"""Tests for `_check_task_ownership`'s dead-pane handling (E-1807, reshaped by E-1898).

The spawn/claim ownership guard used to read a *ghost* owner — a non-ended
`sessions` row pointing at a tmux pane that no longer exists (a session that
died without firing SessionEnd) — as a live collision and refuse the spawn.

E-1807 fixed that by REAPING the ghost first: the guard shelled out to
`endless-go session-query reap-dead-panes`, which wrote the row to `ended`, so
the guard's `state != 'ended'` query then excluded it.

E-1898 removed the reaper. The guard now relies on `_live_sessions`
(`endless-go session-query list-live`), which omits any session whose pane was
observably absent from a tmux server it actually reached. The ghost drops out of
the READ and nothing is written to make that true.

The distinction these tests protect is what the ghost's ABSENCE means:

  - absent because we looked and the pane was gone  -> task is free
  - absent because we could not reach its server    -> STILL OWNED

The second case is why `list-live` reports `unknown` sessions rather than
dropping them. A wrong refusal costs a retry; wrongly freeing a task hands a
live worktree to a second session.
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


def _seed_session(task_id: int, *, pane: str, state: str = "working") -> int:
    """Seed a session owning `task_id`, bound to tmux pane `pane`.

    The binding goes through `processes` (E-1898): a pane id alone is not an
    identity, so the fixture creates the (server, address) row and points
    sessions.process_id at it.
    """
    cur = db.execute(
        "INSERT INTO processes (kind_id, server_uuid, address) VALUES (1, ?, ?)",
        ("test-server-uuid", pane),
    )
    process_id = cur.lastrowid
    cur = db.execute(
        "INSERT INTO sessions (session_id, project_id, platform, state, process_id, "
        "task_id, last_activity) "
        "VALUES (?, 1, 'claude', ?, ?, ?, '2026-07-29T00:00:00')",
        (f"sess-{pane}", state, process_id, task_id),
    )
    return cur.lastrowid


def _session_state(session_id: int) -> str:
    return db.query(
        "SELECT state FROM sessions WHERE id = ?", (session_id,)
    )[0]["state"]


def test_dead_pane_owner_reads_free_without_being_written(
    seeded_project_at_cwd, monkeypatch
):
    """A ghost owner is absent from the live set, so ownership reads free.

    And — the E-1898 part — the ghost row is left exactly as it was. Under the
    old design this test asserted the opposite: that the row had been rewritten
    to 'ended'. Not writing is the point; the write is what could be wrong.
    """
    task_id = _add_task()
    ghost = _seed_session(task_id, pane="%999999")

    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: seeded_project_at_cwd)
    # The Go side observed the pane's server and did not find the pane, so the
    # ghost is simply not reported. (list-live's own filtering is tested in
    # internal/monitor; here we pin the guard's decision given that result.)
    monkeypatch.setattr(session_cmd, "_live_sessions", lambda root: [])

    assert task_cmd._check_task_ownership(task_id, current_eid=None) is False
    assert _session_state(ghost) == "working", (
        "the ghost row was rewritten; reading ownership must not mutate sessions"
    )


def test_live_owner_still_raises(seeded_project_at_cwd, monkeypatch):
    """A genuinely-live different owner still collides."""
    task_id = _add_task()
    live_eid = _seed_session(task_id, pane="%42")

    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: seeded_project_at_cwd)
    monkeypatch.setattr(
        session_cmd, "_live_sessions",
        lambda root: [{"endless_session_id": live_eid, "pane_id": "%42", "liveness": "live"}],
    )

    with pytest.raises(click.ClickException) as excinfo:
        task_cmd._check_task_ownership(task_id, current_eid=None)
    msg = str(excinfo.value)
    assert "already active" in msg
    # The `endless-go tmux reset` escape hatch is GONE: the verb no longer
    # exists, and a genuinely-gone pane no longer produces this error at all.
    assert "tmux reset" not in msg


def test_unreachable_server_owner_still_raises(seeded_project_at_cwd, monkeypatch):
    """An owner we cannot PROVE is alive still holds the task (E-1898, I2).

    This is the case the old reap-first design could not express. The reaper saw
    no pane and wrote 'ended' — freeing the task — whether the pane was gone or
    merely unobservable. Those are different facts, and conflating them is what
    destroyed 59 live bindings on 2026-08-05.
    """
    task_id = _add_task()
    unprovable = _seed_session(task_id, pane="%7")

    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: seeded_project_at_cwd)
    monkeypatch.setattr(
        session_cmd, "_live_sessions",
        lambda root: [{"endless_session_id": unprovable, "pane_id": "%7", "liveness": "unknown"}],
    )

    with pytest.raises(click.ClickException):
        task_cmd._check_task_ownership(task_id, current_eid=None)
    assert _session_state(unprovable) == "working"
