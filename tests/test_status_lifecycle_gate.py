"""`task update --status` surfaces the lifecycle refusal as a clean CLI error.

The rules themselves live in Go — the transition table in
`internal/taskstatus/transitions.go`, the two validators in
`internal/events/status_transition.go`, both covered by Go tests that walk the
table. What is only observable from here is the BOUNDARY: an executor refusal
travels back through `endless-go event emit`'s exit code and stderr, and the
Python side has to turn that into a `ClickException` an agent can read, not a
traceback.

That boundary has its own failure mode. A refusal that arrives as a traceback
tells an agent the tool is broken rather than that the call was wrong, and an
agent told the tool is broken retries.
"""

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str, status: str = "untriaged", task_type: str = "todo") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, (SELECT id FROM task_types WHERE slug = ?), 'now', "
        "datetime('now'))",
        (title, status, task_type),
    )
    return cur.lastrowid


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


def test_the_reported_case_is_refused(seeded_project_at_cwd):
    """An `unplanned`, never-claimed task cannot be reported implemented."""
    tid = _add_task("Add the reported case", status="unplanned")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=tid, status="unverified")

    assert "unverified" in exc.value.message
    assert "unplanned" in exc.value.message
    assert _status(tid) == "unplanned", "the refused write landed anyway"


def test_the_refusal_names_the_reachable_statuses(seeded_project_at_cwd):
    """The caller's next move belongs in the refusal, not in the docs."""
    tid = _add_task("Add a thing to misroute", status="unplanned")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=tid, status="confirmed")

    for reachable in ("submitted", "ready", "underway", "revisit"):
        assert reachable in exc.value.message, (
            f"the refusal does not offer {reachable!r}: {exc.value.message}"
        )


def test_a_legal_edge_still_lands(seeded_project_at_cwd):
    tid = _add_task("Add a thing to route properly", status="untriaged")
    task_cmd.update_plan(item_id=tid, status="unplanned")
    assert _status(tid) == "unplanned"


def test_the_documented_path_runs_unimpeded(seeded_project_at_cwd):
    """A guard that refuses the ordinary workflow is worse than no guard.

    Stops at `underway`: the two work-progress statuses are also actor-checked,
    and this process carries no claiming session. That half is covered in
    internal/events/status_transition_test.go, which can seed one.
    """
    tid = _add_task("Add a thing and walk it", status="untriaged")
    for status in ("unplanned", "submitted", "ready", "underway"):
        task_cmd.update_plan(item_id=tid, status=status)
        assert _status(tid) == status


def test_a_retired_status_can_still_escape(seeded_project_at_cwd):
    """A row holding a status the vocabulary no longer has is not stranded.

    There is no --force, so if the guard refused every move OUT of an unknown
    status, the rows least able to fix themselves would become permanent.
    """
    tid = _add_task("Add a legacy-status thing", status="blocked")
    task_cmd.update_plan(item_id=tid, status="obsolete")
    assert _status(tid) == "obsolete"


def test_blocked_is_not_in_the_vocabulary(seeded_project_at_cwd):
    """Blockedness is the `blocked_by` relation, never a status."""
    tid = _add_task("Add a thing to block")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=tid, status="blocked")
    assert "Invalid status" in exc.value.message
