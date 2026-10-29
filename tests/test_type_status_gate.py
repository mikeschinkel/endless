"""Tests for E-1579: type-aware status-transition gate.

`research` and `epic` tasks never go through user-testable verification — epics
auto-derive to `completed` (E-1541), research ends in `completed --outcome`
(ED-1502). The gate (`_require_status_allowed_for_type` +
`_TYPE_FORBIDDEN_STATUSES` in `task_cmd`) rejects `unverified`/`assumed`/`confirmed`
for those types at every write path that can set a status. This file covers the
`unverified` rejection added by E-1579 and guards the `assumed`/`confirmed`
rejection inherited from E-1577.

The gate is a hard type-correctness invariant: there is no `--force` bypass.
"""

import click
import pytest

from endless import db, task_cmd
from plan_item_steps import itemize, reconcile_as_planned

# task_types seed (internal/schema/schema.sql): 1=task 2=bug 3=research 4=epic.
_TASK = 1
_BUG = 2
_RESEARCH = 3
_EPIC = 4


def _add_task(title: str, status: str = "underway", type_id: int = _TASK) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


# ─── unverified rejected for research/epic via update_plan (E-1579) ───────────────


def test_update_epic_unverified_rejected(seeded_project_at_cwd):
    tid = _add_task("Coordinate the foo epic", type_id=_EPIC)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="unverified")
    msg = str(exc.value.message)
    assert "epic" in msg and "unverified" in msg


def test_update_research_unverified_rejected(seeded_project_at_cwd):
    tid = _add_task("Research the cache strategy", type_id=_RESEARCH)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="unverified")
    assert "unverified" in str(exc.value.message)


# ─── unverified rejected for research/epic via add_item (E-1579) ──────────────────


def test_add_epic_unverified_rejected(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Implement epic", task_type="epic", status="unverified")
    assert "unverified" in str(exc.value.message)


def test_add_research_unverified_rejected(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            "Research X", task_type="research", status="unverified",
            justification="standalone investigation",
        )
    assert "unverified" in str(exc.value.message)


# ─── unverified allowed for task/bug (no regression on normal types) ──────────────


def test_update_task_unverified_allowed(seeded_project_at_cwd):
    tid = _add_task("Implement the widget", type_id=_TASK)
    itemize(tid, approved=True)
    reconcile_as_planned(tid)
    task_cmd.update_plan(tid, status="unverified")
    assert _status(tid) == "unverified"


def test_update_bug_unverified_allowed(seeded_project_at_cwd):
    tid = _add_task("Fix the crash", type_id=_BUG)
    itemize(tid, approved=True)
    reconcile_as_planned(tid)
    task_cmd.update_plan(tid, status="unverified")
    assert _status(tid) == "unverified"


@pytest.mark.parametrize("task_type,title", [("todo", "Implement the widget"),
                                              ("bugfix", "Fix the crash")])
def test_add_implementation_task_at_unverified_refused(task_type, title,
                                                       seeded_project_at_cwd):
    """E-2280: `unverified` is reached only after a reconciliation against the
    task's plan items, which a task being created cannot have."""
    with pytest.raises(click.ClickException, match="cannot be created"):
        task_cmd.add_item(title, task_type=task_type, status="unverified")
    assert not db.query("SELECT id FROM tasks WHERE title = ?", (title,))


# ─── E-1577 inheritance: assumed/confirmed still rejected for research/epic ────


@pytest.mark.parametrize("type_id", [_RESEARCH, _EPIC])
@pytest.mark.parametrize("status", ["assumed", "confirmed"])
def test_update_research_epic_terminal_still_rejected(seeded_project_at_cwd, type_id, status):
    tid = _add_task("Audit the thing", type_id=type_id)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status=status, outcome="findings")
    assert status in str(exc.value.message)


# ─── universal terminals stay allowed for all types ───────────────────────────


@pytest.mark.parametrize("type_id", [_TASK, _BUG, _RESEARCH, _EPIC])
def test_obsolete_allowed_for_all_types(seeded_project_at_cwd, type_id):
    tid = _add_task("Some task", type_id=type_id)
    task_cmd.update_plan(tid, status="obsolete", outcome="no longer needed")
    assert _status(tid) == "obsolete"
