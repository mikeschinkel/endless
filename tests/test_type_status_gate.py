"""Tests for E-1579: type-aware status-transition gate.

`research` and `epic` tasks never go through user-testable verification — epics
auto-derive to `completed` (E-1541), research ends in `completed --outcome`
(ED-1502). The gate (`_require_status_allowed_for_type` +
`_TYPE_FORBIDDEN_STATUSES` in `task_cmd`) rejects `verify`/`assumed`/`confirmed`
for those types at every write path that can set a status. This file covers the
`verify` rejection added by E-1579 and guards the `assumed`/`confirmed`
rejection inherited from E-1577.

The gate is a hard type-correctness invariant: there is no `--force` bypass.
"""

import click
import pytest

from endless import db, task_cmd

# task_types seed (internal/schema/schema.sql): 1=task 2=bug 3=research 4=epic.
_TASK = 1
_BUG = 2
_RESEARCH = 3
_EPIC = 4


def _add_task(title: str, status: str = "in_progress", type_id: int = _TASK) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


# ─── verify rejected for research/epic via update_plan (E-1579) ───────────────


def test_update_epic_verify_rejected(seeded_project_at_cwd):
    tid = _add_task("Coordinate the foo epic", type_id=_EPIC)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="verify")
    msg = str(exc.value.message)
    assert "epic" in msg and "verify" in msg


def test_update_research_verify_rejected(seeded_project_at_cwd):
    tid = _add_task("Research the cache strategy", type_id=_RESEARCH)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="verify")
    assert "verify" in str(exc.value.message)


# ─── verify rejected for research/epic via add_item (E-1579) ──────────────────


def test_add_epic_verify_rejected(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Implement epic", task_type="epic", status="verify")
    assert "verify" in str(exc.value.message)


def test_add_research_verify_rejected(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            "Research X", task_type="research", status="verify",
            justification="standalone investigation",
        )
    assert "verify" in str(exc.value.message)


# ─── verify allowed for task/bug (no regression on normal types) ──────────────


def test_update_task_verify_allowed(seeded_project_at_cwd):
    tid = _add_task("Implement the widget", type_id=_TASK)
    task_cmd.update_plan(tid, status="verify")
    assert _status(tid) == "verify"


def test_update_bug_verify_allowed(seeded_project_at_cwd):
    tid = _add_task("Fix the crash", type_id=_BUG)
    task_cmd.update_plan(tid, status="verify")
    assert _status(tid) == "verify"


def test_add_task_verify_allowed(seeded_project_at_cwd):
    tid = task_cmd.add_item("Implement the widget", task_type="task", status="verify")
    assert _status(tid) == "verify"


def test_add_bug_verify_allowed(seeded_project_at_cwd):
    tid = task_cmd.add_item("Fix the crash", task_type="bug", status="verify")
    assert _status(tid) == "verify"


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
    task_cmd.update_plan(tid, status="obsolete")
    assert _status(tid) == "obsolete"
