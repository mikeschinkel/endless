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


def _add_task(project_id: int, title: str = "T", task_type: str = "todo") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (?, ?, 'unplanned', (SELECT id FROM task_types WHERE slug = ?), 'now', datetime('now'))",
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
    assert legal == ("supersedes", "reverses", "modifies", "documents",
                     "relates_to")


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


# ────────────────────────────────────────────────────────────────────────
# decision update (E-1533) — edit title/description in place
#
# The emit_event path (Go binary) and the .md-mirror rewrite are exercised
# end-to-end by tests/tasks/e-1533-verify.sh against an isolated sandbox.
# These unit tests cover the Python-side validation guards and the payload
# shape (with emit_event / mirror stubbed) so no Go binary is required.
# ────────────────────────────────────────────────────────────────────────


def _stub_update_emit(monkeypatch):
    """Patch emit_event + the .md mirror; return the captured-calls lists."""
    from endless import decision_cmd, event_bridge

    emitted: list = []
    mirrored: list = []
    monkeypatch.setattr(
        event_bridge, "emit_event",
        lambda **kw: emitted.append(kw) or {"id": "ED-1"},
    )
    monkeypatch.setattr(
        decision_cmd, "_mirror_decision_body",
        lambda *a, **kw: mirrored.append((a, kw)),
    )
    return emitted, mirrored


def test_decision_update_unknown_id_errors(isolated_env):
    import click
    _seed_project()
    with pytest.raises(click.ClickException) as exc:
        decision_cmd.update_decision(9999, title="X")
    assert "9999" in str(exc.value.message)


def test_decision_update_no_flags_errors(isolated_env):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D")
    with pytest.raises(click.ClickException) as exc:
        decision_cmd.update_decision(did)
    assert "Nothing to update" in str(exc.value.message)


def test_decision_update_empty_title_errors(isolated_env):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D")
    with pytest.raises(click.ClickException) as exc:
        decision_cmd.update_decision(did, title="   ")
    assert "may not be empty" in str(exc.value.message)


def test_decision_update_record_that_title_errors(isolated_env):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D")
    with pytest.raises(click.ClickException) as exc:
        decision_cmd.update_decision(did, title="record that we chose X")
    assert "state the decision" in str(exc.value.message)


def test_decision_update_emits_fields_and_mirrors(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "Old title")
    emitted, mirrored = _stub_update_emit(monkeypatch)

    decision_cmd.update_decision(did, title="New title", description="new body")

    assert len(emitted) == 1
    ev = emitted[0]
    assert ev["kind"] == "decision.fields_updated"
    assert ev["entity_type"] == "decision"
    assert ev["entity_id"] == str(did)
    assert ev["payload"] == {"fields": {"title": "New title", "description": "new body"}}
    # description changed → mirror rewritten as an "update".
    assert len(mirrored) == 1
    assert mirrored[0][1].get("action") == "update"


def test_decision_update_title_only_skips_mirror(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "Old title")
    emitted, mirrored = _stub_update_emit(monkeypatch)

    decision_cmd.update_decision(did, title="Just the title")

    assert emitted[0]["payload"] == {"fields": {"title": "Just the title"}}
    # No description change → no mirror rewrite (nothing to re-emit).
    assert mirrored == []


def test_decision_update_editable_in_any_status(isolated_env, monkeypatch):
    """Title/description are metadata — correcting them is allowed in any
    status, no proposed-only guard (the whole point of the command)."""
    pid = _seed_project()
    did = _add_decision(pid, "Rejected decision", status="rejected")
    emitted, _ = _stub_update_emit(monkeypatch)

    decision_cmd.update_decision(did, title="Corrected wording")

    assert emitted[0]["payload"] == {"fields": {"title": "Corrected wording"}}


# ────────────────────────────────────────────────────────────────────────
# Status reversals: unaccept / unreject / reconsider (E-1864)
#
# These assert the CLI-side guards and the emitted event kind. The DB
# effect of each kind (including that unreject clears rejection_reason)
# is covered in Go by internal/events/decision_reversal_test.go.
# ────────────────────────────────────────────────────────────────────────


def _stub_status_emit(monkeypatch):
    """Patch emit_event; return the list that captures each call's kwargs."""
    from endless import event_bridge

    emitted: list = []
    monkeypatch.setattr(
        event_bridge, "emit_event",
        lambda **kw: emitted.append(kw) or {"id": "ED-1"},
    )
    return emitted


def test_decision_unaccept_emits_unaccepted(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.unaccept_decision(did)

    assert len(emitted) == 1
    assert emitted[0]["kind"] == "decision.unaccepted"
    assert emitted[0]["entity_type"] == "decision"
    assert emitted[0]["entity_id"] == str(did)
    assert emitted[0]["payload"] == {}


def test_decision_unreject_emits_unrejected(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "D", status="rejected")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.unreject_decision(did)

    assert len(emitted) == 1
    assert emitted[0]["kind"] == "decision.unrejected"
    assert emitted[0]["entity_id"] == str(did)


def test_decision_unaccept_refuses_rejected_and_points_at_unreject(
    isolated_env, monkeypatch
):
    """The wrong-status guard is the reason these are two verbs rather than
    one: aiming unaccept at a rejected decision must error, not silently
    perform the other reversal."""
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="rejected")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.unaccept_decision(did)

    msg = str(exc.value.message)
    assert "'rejected'" in msg
    assert "unreject" in msg
    assert emitted == []


def test_decision_unreject_refuses_accepted_and_points_at_unaccept(
    isolated_env, monkeypatch
):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.unreject_decision(did)

    msg = str(exc.value.message)
    assert "'accepted'" in msg
    assert "unaccept" in msg
    assert emitted == []


@pytest.mark.parametrize("verb", ["unaccept_decision", "unreject_decision",
                                  "reconsider_decision"])
def test_decision_reversals_refuse_proposed(isolated_env, monkeypatch, verb):
    """Nothing to undo on a decision that was never settled."""
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="proposed")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        getattr(decision_cmd, verb)(did)

    assert "'proposed'" in str(exc.value.message)
    assert emitted == []


@pytest.mark.parametrize("verb", ["unaccept_decision", "unreject_decision",
                                  "reconsider_decision"])
def test_decision_reversals_reject_unknown_id(isolated_env, verb):
    import click
    _seed_project()
    with pytest.raises(click.ClickException) as exc:
        getattr(decision_cmd, verb)(9999)
    assert "ED-9999" in str(exc.value.message)


@pytest.mark.parametrize("status,expected_kind", [
    ("accepted", "decision.unaccepted"),
    ("rejected", "decision.unrejected"),
])
def test_decision_reconsider_dispatches_on_status(
    isolated_env, monkeypatch, status, expected_kind
):
    """reconsider is the status-agnostic convenience: it routes to whichever
    reversal applies rather than guarding on one."""
    pid = _seed_project()
    did = _add_decision(pid, "D", status=status)
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.reconsider_decision(did)

    assert len(emitted) == 1
    assert emitted[0]["kind"] == expected_kind


@pytest.mark.parametrize("subcommand", ["unaccept", "unreject", "reconsider"])
def test_decision_reversal_commands_registered(subcommand):
    """The verbs are reachable from the CLI, accept an ED- prefixed id, and
    take more than one (nargs=-1), matching accept/reject."""
    runner = CliRunner()
    result = runner.invoke(main, ["decision", subcommand, "--help"])
    assert result.exit_code == 0
    assert "ITEM_IDS..." in result.output


# ────────────────────────────────────────────────────────────────────────
# End states: supersede / obsolete / reinstate (E-1920)
#
# The CLI-side properties. The executor's own guards are covered by
# internal/events/decision_endstate_test.go; what matters here is that the
# CLI refuses before emitting, that supersede writes BOTH facts (the
# relation and the status), and that the renderers surface the successor —
# a `superseded` row whose replacement is unnameable is the state E-1920
# exists to remove.
# ────────────────────────────────────────────────────────────────────────


def _pid_at_cwd() -> int:
    """The id of the project `seeded_project_at_cwd` registered.

    link/unlink/list all resolve the project from cwd, so the tests that
    exercise them need that fixture rather than a bare `_seed_project()`.
    """
    return db.query("SELECT id FROM projects WHERE name = 'test'")[0]["id"]


def _link_supersedes(new_id: int, old_id: int) -> None:
    """Store `old superseded by new` in its active-voice form: the SOURCE is
    the decision that took over."""
    db.execute(
        "INSERT INTO decision_relations "
        "(source_decision_id, target_kind, target_id, relation_type) "
        "VALUES (?, 'decision', ?, 'supersedes')",
        (new_id, old_id),
    )


def test_supersedes_is_legal_between_decisions():
    decision_cmd.require_legal_relation_type("decision", "decision", "supersedes")


def test_supersedes_is_not_legal_toward_a_task():
    """A task cannot supersede a decision or vice versa — supersession is a
    statement about which rule governs, and a task is not a rule."""
    import click
    with pytest.raises(click.ClickException):
        decision_cmd.require_legal_relation_type("decision", "task", "supersedes")


def test_superseded_by_map_reads_the_stored_direction(isolated_env):
    pid = _seed_project()
    old = _add_decision(pid, "old", status="superseded")
    new = _add_decision(pid, "new", status="accepted")
    _link_supersedes(new, old)

    assert decision_cmd.superseded_by_map([old, new]) == {old: [new]}


def test_superseded_by_map_is_empty_for_an_empty_id_set(isolated_env):
    _seed_project()
    assert decision_cmd.superseded_by_map([]) == {}


def test_superseded_by_map_collects_every_superseder(isolated_env):
    """A decision split into two replacements names both."""
    pid = _seed_project()
    old = _add_decision(pid, "old", status="superseded")
    a = _add_decision(pid, "a", status="accepted")
    b = _add_decision(pid, "b", status="accepted")
    _link_supersedes(a, old)
    _link_supersedes(b, old)

    assert decision_cmd.superseded_by_map([old]) == {old: [a, b]}


def test_superseded_by_note_renders_only_for_superseded():
    assert decision_cmd.superseded_by_note("superseded", [7]) == " (by ED-7)"
    assert decision_cmd.superseded_by_note("accepted", [7]) == ""
    assert decision_cmd.superseded_by_note("obsolete", [7]) == ""


def test_superseded_by_note_is_empty_without_ids():
    assert decision_cmd.superseded_by_note("superseded", None) == ""
    assert decision_cmd.superseded_by_note("superseded", []) == ""


def test_superseded_by_note_joins_multiple():
    assert decision_cmd.superseded_by_note("superseded", [7, 9]) == " (by ED-7, ED-9)"


def test_supersede_emits_relation_then_status(seeded_project_at_cwd, monkeypatch):
    """Both facts, relation first — the status alone cannot carry a pointer."""
    pid = _pid_at_cwd()
    old = _add_decision(pid, "old", status="accepted")
    new = _add_decision(pid, "new", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.supersede_decision(old, new)

    assert [e["kind"] for e in emitted] == [
        "decision_relation.created", "decision.superseded",
    ]
    assert emitted[0]["payload"] == {
        "source_decision_id": new,
        "target_kind": "decision",
        "target_id": old,
        "relation_type": "supersedes",
    }
    assert emitted[1]["entity_id"] == str(old)
    assert emitted[1]["payload"] == {"by_superseding_id": new}


def test_supersede_accepts_a_still_proposed_successor(
    seeded_project_at_cwd, monkeypatch
):
    """Retiring on the strength of a successor that is not yet accepted is
    ordinary; gating it would force an order the work does not have."""
    pid = _pid_at_cwd()
    old = _add_decision(pid, "old", status="accepted")
    new = _add_decision(pid, "new", status="proposed")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.supersede_decision(old, new)

    assert emitted[-1]["kind"] == "decision.superseded"


@pytest.mark.parametrize("status", ["proposed", "rejected", "superseded", "obsolete"])
def test_supersede_refuses_anything_but_accepted(isolated_env, monkeypatch, status):
    """Only an accepted decision governs, so only one can stop governing."""
    import click
    pid = _seed_project()
    old = _add_decision(pid, "old", status=status)
    new = _add_decision(pid, "new", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.supersede_decision(old, new)

    assert f"{status!r}" in str(exc.value.message)
    assert emitted == [], "must refuse BEFORE writing the relation"


@pytest.mark.parametrize("status", ["rejected", "superseded", "obsolete"])
def test_supersede_refuses_a_successor_that_does_not_govern(
    isolated_env, monkeypatch, status
):
    """Closing the old decision behind a successor that never takes effect is
    strictly worse than leaving it accepted."""
    import click
    pid = _seed_project()
    old = _add_decision(pid, "old", status="accepted")
    new = _add_decision(pid, "new", status=status)
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.supersede_decision(old, new)

    assert f"{status!r}" in str(exc.value.message)
    assert emitted == []


def test_supersede_refuses_self(isolated_env, monkeypatch):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.supersede_decision(did, did)

    assert "itself" in str(exc.value.message)
    assert emitted == []


def test_obsolete_emits_obsoleted_with_reason(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.obsolete_decision(did, "the subsystem it governed was deleted")

    assert len(emitted) == 1
    assert emitted[0]["kind"] == "decision.obsoleted"
    assert emitted[0]["entity_id"] == str(did)
    assert emitted[0]["payload"] == {
        "reason": "the subsystem it governed was deleted"
    }


@pytest.mark.parametrize("reason", ["", "   ", None])
def test_obsolete_requires_a_non_empty_reason(isolated_env, monkeypatch, reason):
    """The reason is the only thing separating a rule retired deliberately
    from one that quietly stopped being mentioned."""
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.obsolete_decision(did, reason)

    assert "--reason" in str(exc.value.message)
    assert emitted == []


@pytest.mark.parametrize("status", ["proposed", "rejected", "superseded", "obsolete"])
def test_obsolete_refuses_anything_but_accepted(isolated_env, monkeypatch, status):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status=status)
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.obsolete_decision(did, "gone")

    assert f"{status!r}" in str(exc.value.message)
    assert emitted == []


def test_supersede_refusal_on_proposed_points_at_accept_or_reject(
    isolated_env, monkeypatch
):
    """The three wrong statuses fail for three different reasons, so the
    message says which one it hit and what to do instead."""
    import click
    pid = _seed_project()
    old = _add_decision(pid, "old", status="proposed")
    new = _add_decision(pid, "new", status="accepted")
    _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.supersede_decision(old, new)

    msg = str(exc.value.message)
    assert "never accepted" in msg
    assert "reject" in msg


def test_obsolete_refusal_on_an_end_state_points_at_reinstate(
    isolated_env, monkeypatch
):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status="obsolete")
    _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.obsolete_decision(did, "gone")

    assert "reinstate" in str(exc.value.message)


def test_reinstate_from_obsolete_emits_reinstated(isolated_env, monkeypatch):
    pid = _seed_project()
    did = _add_decision(pid, "D", status="obsolete")
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.reinstate_decision(did)

    assert len(emitted) == 1
    assert emitted[0]["kind"] == "decision.reinstated"
    assert emitted[0]["entity_id"] == str(did)
    assert emitted[0]["payload"] == {}


def test_reinstate_from_superseded_retires_the_relation_first(
    seeded_project_at_cwd, monkeypatch
):
    """A decision back in force must not still carry a successor — that is
    the contradiction E-1920 exists to remove."""
    pid = _pid_at_cwd()
    old = _add_decision(pid, "old", status="superseded")
    new = _add_decision(pid, "new", status="accepted")
    _link_supersedes(new, old)
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.reinstate_decision(old)

    assert [e["kind"] for e in emitted] == [
        "decision_relation.deleted", "decision.reinstated",
    ]
    assert emitted[0]["payload"]["source_decision_id"] == new
    assert emitted[0]["payload"]["relation_type"] == "supersedes"


def test_reinstate_drops_every_superseder(seeded_project_at_cwd, monkeypatch):
    pid = _pid_at_cwd()
    old = _add_decision(pid, "old", status="superseded")
    a = _add_decision(pid, "a", status="accepted")
    b = _add_decision(pid, "b", status="accepted")
    _link_supersedes(a, old)
    _link_supersedes(b, old)
    emitted = _stub_status_emit(monkeypatch)

    decision_cmd.reinstate_decision(old)

    deleted = [e for e in emitted if e["kind"] == "decision_relation.deleted"]
    assert {e["payload"]["source_decision_id"] for e in deleted} == {a, b}


@pytest.mark.parametrize("status", ["proposed", "accepted", "rejected"])
def test_reinstate_refuses_a_live_decision(isolated_env, monkeypatch, status):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status=status)
    emitted = _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.reinstate_decision(did)

    assert f"{status!r}" in str(exc.value.message)
    assert emitted == []


@pytest.mark.parametrize("status", ["accepted", "rejected"])
def test_reinstate_refusal_points_at_reconsider(isolated_env, monkeypatch, status):
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status=status)
    _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.reinstate_decision(did)

    assert "reconsider" in str(exc.value.message)


@pytest.mark.parametrize("status", ["superseded", "obsolete"])
def test_reconsider_refusal_points_at_reinstate(isolated_env, monkeypatch, status):
    """reconsider means 'back on the table'; a retired decision has to regain
    force first, or reinstating a mis-aimed supersede would silently discard
    the accept it should return to."""
    import click
    pid = _seed_project()
    did = _add_decision(pid, "D", status=status)
    _stub_status_emit(monkeypatch)

    with pytest.raises(click.ClickException) as exc:
        decision_cmd.reconsider_decision(did)

    assert "reinstate" in str(exc.value.message)


def test_decision_show_names_the_successor(isolated_env, capsys):
    """The whole point: a superseded decision must say what took over."""
    pid = _seed_project()
    old = _add_decision(pid, "old rule", status="superseded")
    new = _add_decision(pid, "new rule", status="accepted")
    _link_supersedes(new, old)

    decision_cmd.detail_decision(old, llm=True)

    out = capsys.readouterr().out
    assert "status=superseded" in out
    assert f"superseded_by=ED-{new}" in out


def test_decision_show_json_carries_superseded_by(isolated_env, capsys):
    import json
    pid = _seed_project()
    old = _add_decision(pid, "old rule", status="superseded")
    new = _add_decision(pid, "new rule", status="accepted")
    _link_supersedes(new, old)

    decision_cmd.detail_decision(old, as_json=True)

    assert json.loads(capsys.readouterr().out)["superseded_by"] == [f"ED-{new}"]


def test_decision_show_json_superseded_by_is_an_empty_list_not_null(
    isolated_env, capsys
):
    import json
    pid = _seed_project()
    did = _add_decision(pid, "D", status="accepted")

    decision_cmd.detail_decision(did, as_json=True)

    assert json.loads(capsys.readouterr().out)["superseded_by"] == []


def test_decision_list_annotates_the_superseded_row(seeded_project_at_cwd, capsys):
    pid = _pid_at_cwd()
    old = _add_decision(pid, "old rule", status="superseded")
    new = _add_decision(pid, "new rule", status="accepted")
    _link_supersedes(new, old)

    decision_cmd.list_decisions(llm=True)

    out = capsys.readouterr().out
    assert f"superseded (by ED-{new})" in out


@pytest.mark.parametrize("subcommand", ["supersede", "obsolete", "reinstate"])
def test_decision_end_state_commands_registered(subcommand):
    runner = CliRunner()
    result = runner.invoke(main, ["decision", subcommand, "--help"])
    assert result.exit_code == 0
