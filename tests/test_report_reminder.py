"""Tests for the wind-down report reminder (E-1772).

When an agent sets a task to a terminal wind-down status, the status-update
command prints a reminder nudging it to route this handoff — and all further
reporting for the rest of the session — through `endless task report <id>`
(the steering-prompt command built in E-1771), instead of composing a
freeform status message.

The reminder fires ONLY on the agent-driven wind-down transitions:
  - underway -> unverified
  - -> assumed
  - -> completed (with an outcome)

It does NOT fire on submitted, ready, confirmed, the claim's -> underway, or
the revisit/declined/obsolete management transitions.

Nor in a project that turned the report channel off with `"report_gate": false`
(E-1966) — see the last section.
"""

import pytest

from endless import agent_help, config, db, task_cmd

# Stable substring of the reminder — the pointer at the report command.
# The reminder renders `endless task report E-<id>`; this fragment is what
# proves the nudge fired regardless of the surrounding prose.
_MARKER = "endless task report"


@pytest.fixture(autouse=True)
def _agent_gate_open(monkeypatch):
    """Default the reminder's agent gate OPEN so the behavior tests exercise the
    transition logic, not the gate. The gate itself (agent vs. human vs.
    --agent-view) is tested explicitly below, each overriding this. Also pins
    --agent-view OFF so it never leaks in from the ambient CLI state."""
    monkeypatch.setattr(task_cmd, "_running_under_agent", lambda: True)
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


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
    assert f"endless task report E-{tid}" in out


def test_assume_item_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the parser", status="underway")
    task_cmd.assume_item(tid, outcome="believed correct")
    assert _fired(capsys)


def test_update_to_assumed_fires(seeded_project_at_cwd, capsys):
    tid = _add_task("Fix the cache", status="underway")
    task_cmd.update_plan(tid, status="assumed", outcome="believed correct")
    assert _fired(capsys)


def test_mark_completed_item_fires(seeded_project_at_cwd, capsys):
    # E-1658: `completed` is a findings-type terminal; use research (todo/bugfix
    # no longer reach completed). `task complete` bypasses the Go table.
    tid = _add_task("Audit the auth module", status="underway", type_id=3)
    task_cmd.mark_completed_item(tid, outcome="findings: none material")
    assert _fired(capsys)


def test_update_to_completed_fires(seeded_project_at_cwd, capsys):
    # Research reaches completed via the review track; seed at unreviewed so
    # update_plan takes the legal unreviewed→completed edge.
    tid = _add_task("Review the middleware", status="unreviewed", type_id=3)
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


# ─── the agent gate: agent vs. human vs. --agent-view ─────────────────────────
#
# The reminder steers an agent. A human running the same wind-down command
# interactively must not see it; a human can opt in with the global
# --agent-view flag to preview what an agent sees.


def test_agent_session_fires(seeded_project_at_cwd, capsys, monkeypatch):
    monkeypatch.setattr(task_cmd, "_running_under_agent", lambda: True)
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)
    tid = _add_task("Fix the agent-session path", status="underway")
    task_cmd.update_plan(tid, status="unverified")
    assert _fired(capsys)


def test_human_invocation_stays_silent(seeded_project_at_cwd, capsys, monkeypatch):
    monkeypatch.setattr(task_cmd, "_running_under_agent", lambda: False)
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)
    tid = _add_task("Fix the human path", status="underway")
    task_cmd.update_plan(tid, status="unverified")
    assert not _fired(capsys)


def test_agent_view_flag_lets_human_preview(seeded_project_at_cwd, capsys, monkeypatch):
    # Not an agent session, but the human passed --agent-view (sets _AGENT_VIEW).
    monkeypatch.setattr(task_cmd, "_running_under_agent", lambda: False)
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", True)
    tid = _add_task("Fix the agent-view path", status="underway")
    task_cmd.update_plan(tid, status="unverified")
    assert _fired(capsys)


# ─── the report_gate gate (E-1966) ────────────────────────────────────────────
#
# The nudge does not merely advise; it asserts that all further reporting for
# the session goes through `task report`. Where the project set
# `"report_gate": false` nothing routes it and nothing enforces it, so the
# assertion is false — and a session told it is being checked when it is not
# learns that Endless's statements about its own behavior cannot be relied on.
# The Go side already refuses to emit its PostToolUse twin under the same key
# (E-1953, TestReportReinforcement_RespectsTheSwitch); this emitter was missed.
#
# Every test here runs with the agent gate open (autouse fixture above), so a
# silence below is the config key's doing and nothing else.


def _write_gate(project_dir, value) -> None:
    """Give the cwd project a .endless/config.json — with or without the key."""
    cfg = {"name": "test", "status": "active"}
    if value is not None:
        cfg["report_gate"] = value
    config.project_config_write(project_dir, cfg)


def _wind_down(title: str) -> None:
    task_cmd.update_plan(_add_task(title, status="underway"), status="unverified")


def test_report_gate_off_stays_silent(seeded_project_at_cwd, capsys):
    _write_gate(seeded_project_at_cwd, False)
    _wind_down("Fix the gate-off path")
    assert not _fired(capsys)


def test_report_gate_on_still_fires(seeded_project_at_cwd, capsys):
    _write_gate(seeded_project_at_cwd, True)
    _wind_down("Fix the gate-on path")
    assert _fired(capsys)


def test_report_gate_unset_still_fires(seeded_project_at_cwd, capsys):
    # A project that never heard of the setting keeps the channel: only an
    # explicit `false` turns it off (config.project_report_gate's default).
    _write_gate(seeded_project_at_cwd, None)
    _wind_down("Fix the gate-unset path")
    assert _fired(capsys)


def test_unresolvable_project_root_fails_open(seeded_project_at_cwd, capsys, monkeypatch):
    # Ignorance is not an opt-out. Dropping the nudge because a path lookup
    # failed would leave sessions ungoverned in a project that wanted the gate.
    monkeypatch.setattr(config, "enclosing_project_root", lambda cwd=None: None)
    _wind_down("Fix the no-root path")
    assert _fired(capsys)


def test_gate_is_read_from_the_shared_helper(seeded_project_at_cwd, capsys, monkeypatch):
    # The nudge and the spawn handoff ask the same question through the same
    # function — no second copy of the three-line read to drift.
    monkeypatch.setattr(task_cmd, "_report_gate_on", lambda: False)
    _wind_down("Fix the shared-helper path")
    assert not _fired(capsys)
