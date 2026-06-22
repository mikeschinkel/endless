"""Tests for E-1616 / ED-1510: a task may not be both phase=maybe and parented.

The gate logic lives in task_cmd._reject_maybe_with_parent; it is wired into
the three write paths — add_item, update_plan, move_task. Exercised here at the
function level for granular coverage, plus end-to-end via those three entry
points against a real isolated DB. The Go-executor half of the gate is covered
in internal/events/maybe_parent_test.go.
"""

import click
import pytest

from endless import db, task_cmd


def _add_task(
    title: str,
    phase: str = "now",
    status: str = "ready",
    task_type: str = "task",
    parent_id: int | None = None,
) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, "
        "parent_id, created_at) "
        "VALUES (1, ?, ?, (SELECT id FROM task_types WHERE slug = ?), ?, "
        "?, datetime('now'))",
        (title, status, task_type, phase, parent_id),
    )
    return cur.lastrowid


def _phase_and_parent(task_id: int) -> tuple[str, int | None]:
    row = db.query("SELECT phase, parent_id FROM tasks WHERE id = ?", (task_id,))
    return row[0]["phase"], row[0]["parent_id"]


# ---------- helper-level unit tests ----------

def test_reject_fires_on_maybe_with_parent():
    with pytest.raises(click.ClickException) as exc:
        task_cmd._reject_maybe_with_parent("maybe", 5)
    assert "maybe-phase task cannot have a parent" in exc.value.message


def test_reject_inert_on_maybe_root():
    task_cmd._reject_maybe_with_parent("maybe", None)  # no raise


def test_reject_inert_on_committed_phase_with_parent():
    task_cmd._reject_maybe_with_parent("now", 5)  # no raise
    task_cmd._reject_maybe_with_parent("next", 5)  # no raise


def test_reject_inert_on_none_phase():
    task_cmd._reject_maybe_with_parent(None, 5)  # no raise


# ---------- add_item integration ----------

def test_add_maybe_with_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Parent")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Implement maybe child", phase="maybe", parent_id=parent)
    assert "maybe-phase task cannot have a parent" in exc.value.message


def test_add_maybe_root_allowed(seeded_project_at_cwd):
    new_id = task_cmd.add_item("Implement maybe standalone", phase="maybe")
    phase, parent = _phase_and_parent(new_id)
    assert phase == "maybe"
    assert parent is None


def test_add_parented_default_phase_allowed(seeded_project_at_cwd):
    parent = _add_task("Parent")
    new_id = task_cmd.add_item("Implement committed child", parent_id=parent)
    phase, got_parent = _phase_and_parent(new_id)
    assert phase == "now"
    assert got_parent == parent


# ---------- update_plan integration ----------

def test_update_reparent_maybe_refused(seeded_project_at_cwd):
    parent = _add_task("Parent")
    tid = _add_task("Maybe standalone", phase="maybe")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, parent_id=parent)
    assert "maybe-phase task cannot have a parent" in exc.value.message


def test_update_phase_maybe_on_child_refused(seeded_project_at_cwd):
    parent = _add_task("Parent")
    tid = _add_task("Committed child", parent_id=parent)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, phase="maybe")
    assert "maybe-phase task cannot have a parent" in exc.value.message


def test_update_atomic_promote_and_parent_allowed(seeded_project_at_cwd):
    parent = _add_task("Parent")
    tid = _add_task("Maybe standalone", phase="maybe")
    task_cmd.update_plan(tid, phase="next", parent_id=parent)
    phase, got_parent = _phase_and_parent(tid)
    assert phase == "next"
    assert got_parent == parent


def test_update_set_phase_maybe_on_root_allowed(seeded_project_at_cwd):
    """Setting phase=maybe on a parentless task is legal."""
    tid = _add_task("Standalone")
    task_cmd.update_plan(tid, phase="maybe")
    phase, parent = _phase_and_parent(tid)
    assert phase == "maybe"
    assert parent is None


def test_update_unrelated_edit_on_violating_row_allowed(seeded_project_at_cwd):
    """A pre-rule violating row (maybe + parent, inserted directly) stays
    editable for fields other than phase/parent_id — the gate only fires when
    the update itself touches phase or parent_id."""
    parent = _add_task("Parent")
    tid = _add_task("Legacy violation", phase="maybe", parent_id=parent)
    task_cmd.update_plan(tid, description="edited description")  # no raise
    row = db.query("SELECT description FROM tasks WHERE id = ?", (tid,))
    assert row[0]["description"] == "edited description"


# ---------- move_task integration ----------

def test_move_maybe_under_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Parent")
    tid = _add_task("Maybe standalone", phase="maybe")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.move_task(item_id=tid, parent=parent)
    assert "maybe-phase task cannot have a parent" in exc.value.message


def test_move_committed_under_parent_allowed(seeded_project_at_cwd):
    parent = _add_task("Parent")
    tid = _add_task("Committed task", phase="now")
    task_cmd.move_task(item_id=tid, parent=parent)
    _, got_parent = _phase_and_parent(tid)
    assert got_parent == parent


def test_move_maybe_to_root_allowed(seeded_project_at_cwd):
    """Moving a pre-rule violating maybe task to root is the cleanup path."""
    parent = _add_task("Parent")
    tid = _add_task("Legacy violation", phase="maybe", parent_id=parent)
    task_cmd.move_task(item_id=tid, root=True)
    _, got_parent = _phase_and_parent(tid)
    assert got_parent is None
