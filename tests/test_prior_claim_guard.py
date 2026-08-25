"""Tests for E-1967: `task spawn` refuses a task some session already claimed.

`_check_task_ownership` (E-1807/E-1898) already refused a spawn onto a task a
LIVE session holds. That guard misses the case that actually bites: the session
that worked the task ended weeks ago, so nothing is live, and the spawn starts a
second session over from scratch without the first one's reasoning.

`_check_prior_claim` closes it by reading `sessions.task_id` — write-once per
ED-1560, so it is the durable record of who claimed what — with no filter on
session state at all.

These tests exercise the guard directly rather than through `spawn_plan`, which
would drag in tmux, worktree creation and a Claude launch. The one integration
point that matters — that `spawn_plan` calls it, and calls it AFTER the live
check — is pinned at the bottom.
"""

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str = "Target", status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _add_session(
    session_id: int,
    task_id: int | None,
    *,
    state: str = "ended",
    last_activity: str | None = None,
) -> int:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, state, task_id, "
        "started_at, last_activity) "
        "VALUES (?, ?, 1, ?, ?, '2026-08-01T00:00:00', ?)",
        (session_id, f"uuid-{session_id}", state, task_id, last_activity),
    )
    return session_id


def _refusal(item_id: int, status: str = "ready") -> str:
    with pytest.raises(click.ClickException) as excinfo:
        task_cmd._check_prior_claim(item_id, status)
    return excinfo.value.message


# --------------------------------------------------------------------------
# The guard itself
# --------------------------------------------------------------------------


def test_ended_claimant_refuses_and_names_the_session(seeded_project_at_cwd):
    """The whole defect: the prior session is `ended`, so the live-owner check
    saw a free task."""
    tid = _add_task()
    _add_session(1046, tid, state="ended")

    msg = _refusal(tid)
    assert f"E-{tid} was claimed by session ES-1046" in msg
    assert f"endless session goto E-{tid} --resume" in msg
    assert "ES-1046's reasoning" in msg


def test_a_task_no_session_ever_claimed_is_untouched(seeded_project_at_cwd):
    tid = _add_task()
    _add_session(1046, None, state="ended")
    _add_session(1047, _add_task("Someone else's task"), state="working")

    assert task_cmd._check_prior_claim(tid, "ready") is None


def test_revisit_flags_appear_only_for_settled_work(seeded_project_at_cwd):
    """`--revisit` / `--no-revisit` are E-1968's flags and are accepted only on
    a settled task. Rendering them elsewhere would teach a flag the very next
    command rejects."""
    tid = _add_task(status="assumed")
    _add_session(1046, tid)

    settled = _refusal(tid, "assumed")
    assert f"endless session goto E-{tid} --resume --revisit" in settled
    assert "--no-revisit" in settled

    unsettled = _refusal(tid, "revisit")
    assert f"endless session goto E-{tid} --resume\n" in unsettled
    assert "revisit" not in unsettled.split("\n")[1]
    assert "--no-revisit" not in unsettled


def test_several_claimants_name_the_most_recent_and_list_the_rest(
    seeded_project_at_cwd,
):
    """Ordered like monitor.resumeByTask (`last_activity DESC`), which is what
    the command in the message resolves through — so the session it names is the
    session that command lands in."""
    tid = _add_task()
    _add_session(996, tid, last_activity="2026-08-02T00:00:00")
    _add_session(1046, tid, last_activity="2026-08-09T00:00:00")
    _add_session(1029, tid, last_activity="2026-08-05T00:00:00")

    msg = _refusal(tid)
    assert "claimed by session ES-1046" in msg
    assert "Earlier claimants: ES-1029, ES-996" in msg


def test_a_single_claimant_lists_no_others(seeded_project_at_cwd):
    tid = _add_task()
    _add_session(1046, tid)

    assert "Earlier claimants" not in _refusal(tid)


def test_the_refusal_offers_no_escape_hatch(seeded_project_at_cwd):
    """`--force` governs the status demotion, `--new-session` was dropped by
    E-1968, and `task release` is disabled. There is no workaround to document
    and the message must not invent one."""
    tid = _add_task()
    _add_session(1046, tid)

    msg = _refusal(tid)
    assert "--force" not in msg
    assert "--new-session" not in msg
    assert "task release" not in msg


def test_claimants_reads_sessions_not_session_tasks(seeded_project_at_cwd):
    """A session_tasks row is INVOLVEMENT, not ownership: reading a task or
    filing it writes one, and neither is a claim."""
    tid = _add_task()
    _add_session(994, None, state="ended")
    db.execute(
        "INSERT INTO session_tasks (session_id, task_id, relation_id, "
        "created_at, updated_at) VALUES (994, ?, 2, ?, ?)",
        (tid, "2026-08-01T00:00:00", "2026-08-01T00:00:00"),
    )

    assert task_cmd._check_prior_claim(tid, "ready") is None


# --------------------------------------------------------------------------
# Wiring into spawn
# --------------------------------------------------------------------------


def test_spawn_runs_the_live_check_first(seeded_project_at_cwd, monkeypatch):
    """A LIVE owner must still get the live-owner refusal, which is the more
    specific message, not the prior-claim one."""
    import shutil

    order: list[str] = []

    def live_raises(item_id, current_eid):
        order.append("live")
        raise click.ClickException("is already active in session ES-1046")

    monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", live_raises)
    monkeypatch.setattr(
        task_cmd, "_check_prior_claim",
        lambda *a, **k: order.append("prior"),
    )

    tid = _add_task()
    _add_session(1046, tid, state="working")

    with pytest.raises(click.ClickException) as excinfo:
        task_cmd.spawn_plan(tid)

    assert "already active in session" in excinfo.value.message
    assert order == ["live"]


def test_spawn_refuses_when_only_a_dead_session_ever_claimed_it(
    seeded_project_at_cwd, monkeypatch,
):
    """End to end through `spawn_plan`: the live check finds nothing (the
    claimant ended), and the spawn is refused anyway — before any worktree is
    created or any Claude is launched."""
    import shutil

    monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_check_task_ownership", lambda *a, **k: None)

    def must_not_run(**kwargs):
        raise AssertionError("the spawn got past the guard and pre-claimed")

    monkeypatch.setattr(task_cmd, "_perform_claim_work", must_not_run)

    tid = _add_task()
    _add_session(1046, tid, state="ended")

    with pytest.raises(click.ClickException) as excinfo:
        task_cmd.spawn_plan(tid)

    assert "was claimed by session ES-1046" in excinfo.value.message
