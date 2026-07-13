"""Tests for the wind-down report reminder (E-1772).

When an agent sets a task to a terminal wind-down status, the status-update
command prints a reminder nudging it to route this handoff — and all further
reporting for the rest of the session — through `endless task report <id>
--xml` (the structured command built in E-1771), instead of composing a
freeform status message.

The reminder fires ONLY on the agent-driven wind-down transitions:
  - underway -> unverified
  - -> assumed
  - -> completed (with an outcome)

It does NOT fire on submitted, ready, confirmed, the claim's -> underway, or
the revisit/declined/obsolete management transitions.
"""

import pytest

from endless import db, task_cmd

# Stable substring of the reminder — the pointer at the structured command.
# The reminder renders `endless task report E-<id> --xml`; this fragment is
# what proves the nudge fired regardless of the surrounding prose.
_MARKER = "endless task report"


def _add_task(title: str, status: str = "underway", type_id: int = 1) -> int:
    # type_id per task_types seed (internal/schema/schema.sql):
    # 1=task, 2=bug, 3=research, 4=epic, 5=brainstorm.
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _fired(capsys) -> bool:
    return _MARKER in capsys.readouterr().out


# ─── the predicate in isolation ───────────────────────────────────────────────


@pytest.mark.parametrize(
    "old,new,outcome,expected",
    [
        ("underway", "unverified", False, True),
        ("underway", "assumed", True, True),
        ("unverified", "assumed", False, True),
        ("underway", "completed", True, True),
        # completed without an outcome does not fire ("-> completed WITH --outcome").
        ("underway", "completed", False, False),
        # unverified only fires FROM underway; a reopen-style source does not.
        ("confirmed", "unverified", False, False),
        # excluded transitions.
        ("unplanned", "submitted", False, False),
        ("submitted", "ready", False, False),
        ("ready", "underway", False, False),
        ("unverified", "confirmed", False, False),
        ("underway", "declined", False, False),
        ("underway", "obsolete", False, False),
        ("underway", "revisit", False, False),
        # a same-status no-op never fires.
        ("assumed", "assumed", True, False),
    ],
)
def test_is_report_wind_down(old, new, outcome, expected):
    assert task_cmd._is_report_wind_down(old, new, outcome) is expected


# ─── the three firing paths ───────────────────────────────────────────────────


def test_update_underway_to_unverified_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the login bug", status="underway")
    task_cmd.update_plan(tid, status="unverified")
    out = capsys.readouterr().out
    assert _MARKER in out
    assert f"endless task report E-{tid} --xml" in out


def test_assume_item_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the parser", status="underway")
    task_cmd.assume_item(tid, outcome="believed correct")
    assert _fired(capsys)


def test_update_to_assumed_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the cache", status="underway")
    task_cmd.update_plan(tid, status="assumed", outcome="believed correct")
    assert _fired(capsys)


def test_mark_completed_item_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Audit the auth module", status="underway")
    task_cmd.mark_completed_item(tid, outcome="findings: none material")
    assert _fired(capsys)


def test_update_to_completed_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Review the middleware", status="underway")
    task_cmd.update_plan(tid, status="completed", outcome="sound; no changes")
    assert _fired(capsys)


# ─── the excluded transitions do NOT fire ─────────────────────────────────────


def test_claim_to_underway_does_not_fire(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the router", status="ready")
    task_cmd.update_plan(tid, status="underway")
    assert not _fired(capsys)


def test_confirm_does_not_fire(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the timeout", status="unverified")
    task_cmd.complete_item(tid)
    assert not _fired(capsys)


def test_submit_does_not_fire(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the leak", status="unplanned")
    task_cmd.submit_item(tid)
    assert not _fired(capsys)


def test_decline_does_not_fire(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the flake", status="underway")
    task_cmd.decline_item(tid, reason="not doing it")
    assert not _fired(capsys)


def test_non_status_update_does_not_fire(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the icon", status="underway")
    task_cmd.update_plan(tid, description="new description")
    assert not _fired(capsys)
