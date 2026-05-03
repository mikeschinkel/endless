"""Tests for E-1139: decision-to-decision link CLI and reverses/modifies types.

Covers:
- New stored types reverses/modifies in CANONICAL_DEP_TYPES, STORED_DEP_TYPES,
  RELATION_DISPLAY_ORDER, RELATION_LABELS (E-1156).
- link_tasks/unlink_tasks accepting reverses/reversed_by and
  modifies/modified_by (no schema migration needed; column is permissive).
- 'endless decision link' / 'endless decision unlink' validate both args
  are decisions, refuse otherwise.
"""

import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _add(title: str, type_: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, "needs_plan" if type_ == "task" else "confirmed", type_),
    )
    return cur.lastrowid


def _seed(isolated_env):
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', '/tmp/test', 'active', datetime('now'), datetime('now'))"
    )


# ---------------------------------------------------------------------------
# Registry membership (E-1156)
# ---------------------------------------------------------------------------

def test_reverses_in_canonical_registries():
    assert task_cmd.CANONICAL_DEP_TYPES["reverses"] == ("reverses", False)
    assert task_cmd.CANONICAL_DEP_TYPES["reversed_by"] == ("reverses", True)
    assert "reverses" in task_cmd.STORED_DEP_TYPES
    assert "reverses" in task_cmd.RELATION_DISPLAY_ORDER
    assert "reversed_by" in task_cmd.RELATION_DISPLAY_ORDER
    assert task_cmd.RELATION_LABELS["reverses"] == "Reverses"
    assert task_cmd.RELATION_LABELS["reversed_by"] == "Reversed by"


def test_modifies_in_canonical_registries():
    assert task_cmd.CANONICAL_DEP_TYPES["modifies"] == ("modifies", False)
    assert task_cmd.CANONICAL_DEP_TYPES["modified_by"] == ("modifies", True)
    assert "modifies" in task_cmd.STORED_DEP_TYPES
    assert "modifies" in task_cmd.RELATION_DISPLAY_ORDER
    assert "modified_by" in task_cmd.RELATION_DISPLAY_ORDER
    assert task_cmd.RELATION_LABELS["modifies"] == "Modifies"
    assert task_cmd.RELATION_LABELS["modified_by"] == "Modified by"


def test_clarifies_and_reaffirms_deferred():
    """E-1157 (maybe phase): not in canonical vocab today."""
    assert "clarifies" not in task_cmd.CANONICAL_DEP_TYPES
    assert "reaffirms" not in task_cmd.CANONICAL_DEP_TYPES


# ---------------------------------------------------------------------------
# link_tasks/unlink_tasks accept the new types
# ---------------------------------------------------------------------------

def test_link_reverses_no_swap(isolated_env):
    _seed(isolated_env)
    new_d = _add("New decision", "decision")
    old_d = _add("Old decision", "decision")
    task_cmd.link_tasks(new_d, old_d, "reverses")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == new_d
    assert rows[0]["target_id"] == old_d
    assert rows[0]["dep_type"] == "reverses"


def test_link_reversed_by_swaps(isolated_env):
    _seed(isolated_env)
    old_d = _add("Old decision", "decision")
    new_d = _add("New decision", "decision")
    task_cmd.link_tasks(old_d, new_d, "reversed_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == new_d
    assert rows[0]["target_id"] == old_d
    assert rows[0]["dep_type"] == "reverses"


def test_link_modifies_no_swap(isolated_env):
    _seed(isolated_env)
    new_d = _add("New decision", "decision")
    old_d = _add("Old decision", "decision")
    task_cmd.link_tasks(new_d, old_d, "modifies")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["dep_type"] == "modifies"


def test_link_modified_by_swaps(isolated_env):
    _seed(isolated_env)
    old_d = _add("Old decision", "decision")
    new_d = _add("New decision", "decision")
    task_cmd.link_tasks(old_d, new_d, "modified_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == new_d
    assert rows[0]["target_id"] == old_d
    assert rows[0]["dep_type"] == "modifies"


# ---------------------------------------------------------------------------
# require_decision_pair validation
# ---------------------------------------------------------------------------

def test_require_decision_pair_accepts_two_decisions(isolated_env):
    _seed(isolated_env)
    a = _add("A", "decision")
    b = _add("B", "decision")
    # No exception
    task_cmd.require_decision_pair(a, b)


def test_require_decision_pair_rejects_task_source(isolated_env):
    _seed(isolated_env)
    t = _add("T", "task")
    d = _add("D", "decision")
    import click as _click
    with pytest.raises(_click.ClickException) as exc:
        task_cmd.require_decision_pair(t, d)
    assert f"E-{t}" in str(exc.value.message)
    assert "type='task'" in str(exc.value.message)


def test_require_decision_pair_rejects_task_target(isolated_env):
    _seed(isolated_env)
    d = _add("D", "decision")
    t = _add("T", "task")
    import click as _click
    with pytest.raises(_click.ClickException):
        task_cmd.require_decision_pair(d, t)


def test_require_decision_pair_rejects_missing_id(isolated_env):
    _seed(isolated_env)
    d = _add("D", "decision")
    import click as _click
    with pytest.raises(_click.ClickException) as exc:
        task_cmd.require_decision_pair(d, 99999)
    assert "not found" in str(exc.value.message)


# ---------------------------------------------------------------------------
# CLI integration: 'endless decision link' / 'unlink'
# ---------------------------------------------------------------------------

def test_decision_link_succeeds_for_two_decisions(isolated_env):
    _seed(isolated_env)
    new_d = _add("New decision", "decision")
    old_d = _add("Old decision", "decision")
    runner = CliRunner()
    result = runner.invoke(main, [
        "decision", "link", str(new_d), "--to", str(old_d), "--type", "reverses",
    ])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT dep_type FROM task_deps"))
    assert rows[0]["dep_type"] == "reverses"


def test_decision_link_rejects_task_argument(isolated_env):
    _seed(isolated_env)
    t = _add("Task", "task")
    d = _add("Decision", "decision")
    runner = CliRunner()
    result = runner.invoke(main, [
        "decision", "link", str(t), "--to", str(d), "--type", "reverses",
    ])
    assert result.exit_code != 0
    assert "decisions" in result.output
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_decision_unlink_succeeds_for_two_decisions(isolated_env):
    _seed(isolated_env)
    new_d = _add("New decision", "decision")
    old_d = _add("Old decision", "decision")
    task_cmd.link_tasks(new_d, old_d, "reverses")

    runner = CliRunner()
    result = runner.invoke(main, [
        "decision", "unlink", str(new_d), "--to", str(old_d), "--type", "reverses",
    ])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_decision_unlink_rejects_task_argument(isolated_env):
    _seed(isolated_env)
    t = _add("Task", "task")
    d = _add("Decision", "decision")
    runner = CliRunner()
    result = runner.invoke(main, [
        "decision", "unlink", str(t), "--to", str(d), "--type", "reverses",
    ])
    assert result.exit_code != 0
    assert "decisions" in result.output


def test_task_link_remains_permissive(isolated_env):
    """endless task link still works between a task and a decision (E-980 etc.)."""
    _seed(isolated_env)
    t = _add("Task", "task")
    d = _add("Decision", "decision")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "link", str(d), "--to", str(t), "--type", "documents",
    ])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT dep_type FROM task_deps"))
    assert rows[0]["dep_type"] == "documents"
