"""Tests for decision_cmd — decisions-table CLI surface (E-1507).

After E-1378 + E-1507 the decisions live in their own table (`decisions`)
with their own ID space (display: ED-NN). decision_relations is the
source-table for decision-sourced relations. task_deps holds task-sourced
relations including task→decision (target_type='decision').

These tests insert rows directly to bypass the Go event binary in unit
tests; integration coverage of the event path is exercised by the
sandbox E2E in the task plan's Verification section.
"""

import pytest
from click.testing import CliRunner

from endless import db, decision_cmd
from endless.cli import main


def _seed_project(name: str = "test") -> int:
    cur = db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES (?, '/tmp/test', 'active', datetime('now'), datetime('now'))",
        (name,),
    )
    return cur.lastrowid


def _add_task(project_id: int, title: str = "T", task_type: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (?, ?, 'needs_plan', (SELECT id FROM task_types WHERE slug = ?), 'now', datetime('now'))",
        (project_id, title, task_type),
    )
    return cur.lastrowid


def _add_decision(
    project_id: int, title: str = "D", status: str = "proposed"
) -> int:
    cur = db.execute(
        "INSERT INTO decisions (project_id, title, description, status, "
        "created_at, updated_at) "
        "VALUES (?, ?, '', ?, datetime('now'), datetime('now'))",
        (project_id, title, status),
    )
    return cur.lastrowid


# ────────────────────────────────────────────────────────────────────────
# ID display + parsing
# ────────────────────────────────────────────────────────────────────────


def test_decision_id_display():
    assert decision_cmd.decision_id_display(42) == "ED-42"


def test_decision_id_click_type_accepts_prefixed_and_bare():
    from endless.cli import DECISION_ID

    assert DECISION_ID.convert("ED-7", None, None) == 7
    assert DECISION_ID.convert("ed-7", None, None) == 7
    assert DECISION_ID.convert("7", None, None) == 7


def test_decision_id_click_type_rejects_task_prefix():
    from endless.cli import DECISION_ID
    import click

    with pytest.raises(click.BadParameter):
        DECISION_ID.convert("E-7", None, None)


def test_task_id_click_type_rejects_decision_prefix():
    from endless.cli import TASK_ID
    import click

    with pytest.raises(click.BadParameter):
        TASK_ID.convert("ED-7", None, None)


def test_task_or_decision_id_dispatches_on_prefix():
    from endless.cli import TASK_OR_DECISION_ID

    assert TASK_OR_DECISION_ID.convert("E-42", None, None) == ("task", 42)
    assert TASK_OR_DECISION_ID.convert("ED-42", None, None) == ("decision", 42)
    assert TASK_OR_DECISION_ID.convert("42", None, None) == ("task", 42)


# ────────────────────────────────────────────────────────────────────────
# Relation-type vocabulary by pair
# ────────────────────────────────────────────────────────────────────────


def test_legal_decision_to_task_types():
    legal = decision_cmd.LEGAL_TYPES_BY_PAIR[("decision", "task")]
    assert legal == ("documents", "cleans_up_by", "implemented_by", "relates_to")


def test_legal_decision_to_decision_types():
    legal = decision_cmd.LEGAL_TYPES_BY_PAIR[("decision", "decision")]
    assert legal == ("reverses", "modifies", "documents", "relates_to")


def test_legal_task_to_decision_types():
    legal = decision_cmd.LEGAL_TYPES_BY_PAIR[("task", "decision")]
    assert legal == ("implements", "cleans_up", "documents", "relates_to")


def test_require_legal_relation_type_accepts_legal():
    decision_cmd.require_legal_relation_type("decision", "task", "documents")
    decision_cmd.require_legal_relation_type("decision", "decision", "reverses")
    decision_cmd.require_legal_relation_type("task", "decision", "implements")


def test_require_legal_relation_type_rejects_illegal():
    import click

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.require_legal_relation_type("decision", "task", "blocks")
    assert "not legal" in str(exc.value.message)
    assert "decision→task" in str(exc.value.message)


# ────────────────────────────────────────────────────────────────────────
# CLI: decision list
# ────────────────────────────────────────────────────────────────────────


def test_decision_list_empty(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "list", "--project", "test"])
    assert result.exit_code == 0, result.output
    assert "No decisions" in result.output


def test_decision_list_shows_ed_prefix(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "Why X over Y", "proposed")
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "list", "--project", "test"])
    assert result.exit_code == 0, result.output
    assert f"ED-{did}" in result.output
    assert "Why X over Y" in result.output
    assert "proposed" in result.output


def test_decision_list_json(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "Title", "accepted")
    runner = CliRunner()
    result = runner.invoke(
        main, ["decision", "list", "--project", "test", "--json"]
    )
    assert result.exit_code == 0, result.output
    import json

    out = json.loads(result.output)
    assert out[0]["id"] == f"ED-{did}"
    assert out[0]["status"] == "accepted"


# ────────────────────────────────────────────────────────────────────────
# CLI: decision show (uses decision_cmd.detail_decision, no event emit)
# ────────────────────────────────────────────────────────────────────────


def test_decision_show_human(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "Pick library X", "accepted")
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "show", str(did)])
    assert result.exit_code == 0, result.output
    assert f"ED-{did}" in result.output
    assert "Pick library X" in result.output
    assert "accepted" in result.output


def test_decision_show_unknown_errors(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "show", "ED-9999"])
    assert result.exit_code != 0
    assert "ED-9999" in result.output


def test_decision_show_renders_rejection_reason(isolated_env):
    pid = _seed_project()
    cur = db.execute(
        "INSERT INTO decisions (project_id, title, description, status, "
        "rejection_reason, created_at, updated_at) "
        "VALUES (?, 'X', '', 'rejected', 'too expensive', "
        "datetime('now'), datetime('now'))",
        (pid,),
    )
    did = cur.lastrowid
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "show", str(did)])
    assert result.exit_code == 0
    assert "too expensive" in result.output


def test_decision_show_json_includes_relations(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "D", "accepted")
    tid = _add_task(pid, "T")
    db.execute(
        "INSERT INTO decision_relations "
        "(source_decision_id, target_kind, target_id, relation_type) "
        "VALUES (?, 'task', ?, 'documents')",
        (did, tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["decision", "show", str(did), "--json"])
    assert result.exit_code == 0, result.output
    import json

    out = json.loads(result.output)
    rels = out["relations"]
    assert len(rels) == 1
    assert rels[0]["kind"] == "task"
    assert rels[0]["id"] == f"E-{tid}"
    assert rels[0]["type"] == "documents"
    assert rels[0]["direction"] == "out"


# ────────────────────────────────────────────────────────────────────────
# task add no longer accepts --decision
# ────────────────────────────────────────────────────────────────────────


def test_task_add_decision_flag_removed(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(
        main, ["task", "add", "X", "--decision", "rationale"]
    )
    assert result.exit_code != 0
    assert "--decision" in result.output or "no such option" in result.output.lower()


def test_task_update_decision_flag_removed(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(
        main,
        ["task", "update", "E-1", "--decision", "rationale"],
    )
    assert result.exit_code != 0
    assert "--decision" in result.output or "no such option" in result.output.lower()


def test_task_type_choice_no_longer_includes_decision(isolated_env):
    _seed_project()
    runner = CliRunner()
    # --type decision must be rejected by Click's Choice validation BEFORE
    # add_item runs (so we never hit the Go event binary).
    result = runner.invoke(
        main, ["task", "add", "Title", "--type", "decision"]
    )
    assert result.exit_code != 0
    assert "decision" in result.output.lower()


# ────────────────────────────────────────────────────────────────────────
# task confirm/assume redirect on ED- IDs
# ────────────────────────────────────────────────────────────────────────


def test_task_confirm_on_ed_id_redirects(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["task", "confirm", "ED-42"])
    assert result.exit_code != 0
    msg = result.output.lower()
    assert "decision" in msg
    assert "ed-42" in msg or "accept" in msg


def test_task_assume_on_ed_id_redirects(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["task", "assume", "ED-7"])
    assert result.exit_code != 0
    msg = result.output.lower()
    assert "decision" in msg


def test_task_show_on_ed_id_redirects(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", "ED-7"])
    assert result.exit_code != 0
    assert "decision" in result.output.lower()


# ────────────────────────────────────────────────────────────────────────
# decision link / unlink validation
# ────────────────────────────────────────────────────────────────────────


def test_decision_link_rejects_illegal_type_for_pair(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "D")
    tid = _add_task(pid, "T")
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "decision", "link", f"ED-{did}",
            "--to", f"E-{tid}",
            "--type", "blocks",
        ],
    )
    assert result.exit_code != 0
    assert "decision" in result.output.lower()
    assert "blocks" in result.output


def test_decision_link_rejects_missing_source(isolated_env):
    pid = _seed_project()
    tid = _add_task(pid, "T")
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "decision", "link", "ED-9999",
            "--to", f"E-{tid}",
            "--type", "documents",
        ],
    )
    assert result.exit_code != 0
    assert "ED-9999" in result.output


def test_decision_link_rejects_missing_target(isolated_env):
    pid = _seed_project()
    did = _add_decision(pid, "D")
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "decision", "link", f"ED-{did}",
            "--to", "E-9999",
            "--type", "documents",
        ],
    )
    assert result.exit_code != 0
    assert "E-9999" in result.output


# ────────────────────────────────────────────────────────────────────────
# task link dispatches to decision_cmd helpers on ED- target
# ────────────────────────────────────────────────────────────────────────


def test_task_link_to_decision_writes_task_deps_row(isolated_env):
    pid = _seed_project()
    tid = _add_task(pid, "T")
    did = _add_decision(pid, "D")
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "task", "link", f"E-{tid}",
            "--to", f"ED-{did}",
            "--type", "implements",
        ],
    )
    assert result.exit_code == 0, result.output
    rows = list(
        db.query(
            "SELECT source_type, source_id, target_type, target_id, dep_type "
            "FROM task_deps"
        )
    )
    assert len(rows) == 1
    assert rows[0]["source_type"] == "task"
    assert rows[0]["source_id"] == tid
    assert rows[0]["target_type"] == "decision"
    assert rows[0]["target_id"] == did
    assert rows[0]["dep_type"] == "implements"
    assert (
        f"Task E-{tid} implements Decision ED-{did}" in result.output
    )


def test_task_link_to_decision_rejects_illegal_type(isolated_env):
    pid = _seed_project()
    tid = _add_task(pid, "T")
    did = _add_decision(pid, "D")
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "task", "link", f"E-{tid}",
            "--to", f"ED-{did}",
            "--type", "blocks",
        ],
    )
    assert result.exit_code != 0
    assert "blocks" in result.output


def test_task_unlink_to_decision_removes_task_deps_row(isolated_env):
    pid = _seed_project()
    tid = _add_task(pid, "T")
    did = _add_decision(pid, "D")
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'decision', ?, 'implements')",
        (tid, did),
    )
    runner = CliRunner()
    result = runner.invoke(
        main,
        [
            "task", "unlink", f"E-{tid}",
            "--to", f"ED-{did}",
            "--type", "implements",
        ],
    )
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []
