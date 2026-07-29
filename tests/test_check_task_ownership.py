"""Tests for `_check_task_ownership`'s dead-pane self-heal (E-1807).

The spawn/claim ownership guard used to read a *ghost* owner — a non-ended
`sessions` row pointing at a tmux pane that no longer exists (a session that
died without firing SessionEnd) — as a live collision and refuse the spawn.
E-1807 has the guard reap that ghost (`monitor.ReapDeadTmuxPanes`, exposed as
`endless-go session-query reap-dead-panes`) *before* it reads ownership, so the
`state != 'ended'` query naturally excludes it and the task reads as free.

These tests stand in for the Go reaper (its own binary test lives in
`internal/sessionquerycmd`) by faking `_reap_dead_panes` to apply the same DB
effect, and assert the guard's resulting decision.
"""

import click
import pytest

from endless import db, session_cmd, task_cmd


def _add_task(title: str = "Build e1807 owned task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'underway', 1, 'now', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


def _seed_session(task_id: int, *, process: str, state: str = "working") -> int:
    """Seed a session owning `task_id` via tmux pane `process`; return its id."""
    cur = db.execute(
        "INSERT INTO sessions (session_id, project_id, platform, state, process, "
        "active_task_id, last_activity) "
        "VALUES (?, 1, 'claude', ?, ?, ?, '2026-07-29T00:00:00')",
        (f"sess-{process}", state, process, task_id),
    )
    return cur.lastrowid


def _session_state(session_id: int) -> str:
    return db.query(
        "SELECT state FROM sessions WHERE id = ?", (session_id,)
    )[0]["state"]


def test_dead_pane_owner_is_reaped_and_task_reads_free(
    seeded_project_at_cwd, monkeypatch
):
    """A ghost owner (dead pane) is reaped first, so ownership reads free."""
    task_id = _add_task()
    ghost = _seed_session(task_id, process="%999999")

    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: seeded_project_at_cwd)

    # Stand in for the Go reaper: end the ghost row (as ReapDeadTmuxPanes would
    # for a pane absent from the live tmux server).
    def fake_reap(project_root):
        db.execute(
            "UPDATE sessions SET state='ended', process=NULL WHERE id = ?",
            (ghost,),
        )

    monkeypatch.setattr(session_cmd, "_reap_dead_panes", fake_reap)
    # Guard against a false pass: if the reap somehow didn't take, this would
    # surface the ghost as live and raise instead of returning False.
    monkeypatch.setattr(
        session_cmd, "_live_sessions",
        lambda root: pytest.fail("live set consulted despite reaped ghost"),
    )

    assert task_cmd._check_task_ownership(task_id, current_eid=None) is False
    assert _session_state(ghost) == "ended"


def test_live_owner_still_raises_after_reap(seeded_project_at_cwd, monkeypatch):
    """A genuinely-live different owner still collides (reap is a no-op here)."""
    task_id = _add_task()
    live_eid = _seed_session(task_id, process="%42")

    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: seeded_project_at_cwd)
    monkeypatch.setattr(session_cmd, "_reap_dead_panes", lambda root: None)
    monkeypatch.setattr(
        session_cmd, "_live_sessions",
        lambda root: [{"endless_session_id": live_eid, "pane_id": "%42"}],
    )

    with pytest.raises(click.ClickException) as excinfo:
        task_cmd._check_task_ownership(task_id, current_eid=None)
    # The retained escape-hatch line points at the manual reset fallback.
    assert "endless-go tmux reset" in str(excinfo.value)


def test_reap_dead_panes_swallows_subprocess_errors(seeded_project_at_cwd, monkeypatch):
    """`_reap_dead_panes` must never raise: a reaper error can't block a spawn."""
    import subprocess

    def boom(*args, **kwargs):
        raise FileNotFoundError("endless-go not on PATH")

    monkeypatch.setattr(subprocess, "run", boom)
    # Returns None (no raise) so the ownership guard falls through unaffected.
    assert session_cmd._reap_dead_panes(seeded_project_at_cwd) is None
