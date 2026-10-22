"""A task is continued only from its earlier session (E-2418).

`task spawn` and `task prime` already refuse a task some session holds or held
(`_check_task_ownership`, `_check_prior_claim`, E-1967). `task claim` checked
only for a LIVE holder, and `task bind` checked neither — so either could start
a task over in a second session, with a clean transcript, while the session
that did the work sat ended (or still running) with the reasoning in it.

Both now refuse, name the most recent holder and `session goto E-N --resume`,
and change nothing. `--new-transcript` — the user's explicit fresh start — is
in the human's text only, never the agent's.
"""

import os

import click
import pytest

from endless import agent_help, db, session_cmd, task_cmd


def _add_task(status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, description, status, type_id, "
        "phase, created_at) VALUES (1, 'Held task', 'Held task', ?, 1, 'now', "
        "datetime('now'))",
        (status,),
    )
    tid = cur.lastrowid
    db.execute(
        "INSERT INTO task_content (task_id, name, content) "
        "VALUES (?, 'plan', '# Plan')",
        (tid,),
    )
    return tid


def _add_session(eid: int, task_id: int | None, *, state: str = "ended",
                 last_activity: str = "2026-10-01T00:00:00") -> int:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "task_id, started_at, last_activity) "
        "VALUES (?, ?, 1, 'claude', ?, ?, '2026-10-01T00:00:00', ?)",
        (eid, f"uuid-{eid}", state, task_id, last_activity),
    )
    return eid


def _task_id_of(eid: int):
    return db.query("SELECT task_id FROM sessions WHERE id = ?", (eid,))[0]["task_id"]


def _status(tid: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]["status"]


@pytest.fixture
def as_session(seeded_project_at_cwd, monkeypatch):
    """Make claim and bind resolve to session `eid`, run from another pane, and
    report no live sessions (a holder is then only ever seen as a prior one)."""
    def _use(eid: int) -> None:
        monkeypatch.setattr(
            task_cmd, "_resolve_session_id_with_prompt", lambda **kw: eid,
        )
        monkeypatch.setattr(task_cmd, "_resolve_bind_session", lambda *a: eid)
        monkeypatch.setattr(task_cmd, "_in_claude_session", lambda: False)
        monkeypatch.setattr(session_cmd, "_live_sessions", lambda root: [])
    return _use


def _refusal(fn, *args) -> agent_help.Refusal:
    with pytest.raises(click.ClickException) as excinfo:
        fn(*args)
    return excinfo.value


# ── P1: claim ────────────────────────────────────────────────────────────────

@pytest.mark.parametrize("holder_state", ["ended", "working"])
def test_claim_refuses_a_task_another_session_held(as_session, holder_state):
    tid = _add_task()
    _add_session(1046, tid, state=holder_state)
    _add_session(1100, None, state="working")
    as_session(1100)

    exc = _refusal(task_cmd.claim_item, tid)

    msg = exc.format_message()
    assert "ES-1046" in msg
    assert f"endless session goto E-{tid} --resume" in msg
    assert "Nothing was claimed" in exc.summary
    assert _task_id_of(1100) is None, "the claimer must not have been bound"
    assert _status(tid) == "ready", "the task must not have moved"


def test_claim_refuses_an_unattended_claim_of_a_held_task(as_session):
    tid = _add_task()
    _add_session(1046, tid)
    as_session(1100)

    _refusal(task_cmd.claim_item, tid, True)
    assert _status(tid) == "ready"


# ── P2: bind ─────────────────────────────────────────────────────────────────

@pytest.mark.parametrize("holder_state", ["ended", "working"])
def test_bind_refuses_a_task_another_session_held(as_session, holder_state):
    tid = _add_task(status="assumed")
    _add_session(1046, tid, state=holder_state)
    _add_session(1100, None, state="working")
    as_session(1100)

    exc = _refusal(task_cmd.bind_item, tid)

    msg = exc.format_message()
    assert "ES-1046" in msg
    assert f"endless session goto E-{tid} --resume" in msg
    assert "Nothing was bound" in exc.summary
    assert _task_id_of(1100) is None


def test_the_refusal_names_the_most_recent_holder(as_session):
    tid = _add_task()
    _add_session(996, tid, last_activity="2026-10-02T00:00:00")
    _add_session(1046, tid, last_activity="2026-10-09T00:00:00")
    _add_session(1100, None, state="working")
    as_session(1100)

    msg = _refusal(task_cmd.bind_item, tid).format_message()
    assert "held by session ES-1046" in msg
    assert "ES-996" in msg


# ── P3: your own task ────────────────────────────────────────────────────────

def test_binding_your_own_task_again_is_not_refused(as_session, capsys):
    tid = _add_task()
    _add_session(1100, tid, state="working")
    as_session(1100)

    task_cmd.bind_item(tid)
    assert "already bound" in capsys.readouterr().out


def test_claiming_a_task_you_hold_is_not_refused(as_session, monkeypatch):
    """E-1994's primed session: it bound from its worktree, so it already holds
    the task, and its claim is what starts it."""
    tid = _add_task()
    _add_session(1100, tid, state="working")
    as_session(1100)
    performed = []
    monkeypatch.setattr(
        task_cmd, "_perform_claim_work",
        lambda **kw: performed.append(kw) or (None, False),
    )

    task_cmd.claim_item(tid)
    assert performed and performed[0]["target_session"] == 1100


# ── P4: --new-transcript is the human's remedy only ──────────────────────────

@pytest.mark.parametrize("verb", ["claim_item", "bind_item"])
def test_new_transcript_is_offered_to_a_person_only(as_session, monkeypatch, verb):
    tid = _add_task()
    _add_session(1046, tid)
    _add_session(1100, None, state="working")
    as_session(1100)

    exc = _refusal(getattr(task_cmd, verb), tid)

    monkeypatch.setattr(agent_help, "agent_facing", lambda: False)
    assert "--new-transcript" in exc.format_message()
    monkeypatch.setattr(agent_help, "agent_facing", lambda: True)
    assert "--new-transcript" not in exc.format_message()


# ── P7: --new-transcript marks its launch ────────────────────────────────────

class _Exec(Exception):
    pass


class _Res:
    returncode, stderr = 0, ""

    def __init__(self, stdout=""):
        self.stdout = stdout


@pytest.fixture
def resume_harness(monkeypatch, tmp_path):
    wt = tmp_path / "wt"
    wt.mkdir()
    trace: dict = {}
    target = {
        "endless_id": 99, "session_id": "dead-beef-uuid", "task_id": 10,
        "project_id": 3, "worktree_path": str(wt), "state": "ended",
        "task_type": "todo", "task_status": "underway",
        "task_title": "Resume me", "landed_sha": "",
    }
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setenv("TMUX_PANE", "%42")
    monkeypatch.delenv(session_cmd.NEW_TRANSCRIPT_ENV, raising=False)
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: target)
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd, "_require_transcript", lambda *a: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%42"])
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)

    def fake_tmux(args, timeout=2.0):
        if args and args[0] == "new-window":
            trace["window"] = list(args)
            return _Res("%77\n")
        if args and args[-1] == "#{session_id}":
            return _Res("$0\n")
        return _Res()

    def fake_exec(file, argv):
        trace["exec"] = list(argv)
        trace["env"] = os.environ.get(session_cmd.NEW_TRANSCRIPT_ENV)
        raise _Exec()

    monkeypatch.setattr(session_cmd, "_tmux_run", fake_tmux)
    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    yield trace
    os.environ.pop(session_cmd.NEW_TRANSCRIPT_ENV, None)


def test_session_resume_new_transcript_marks_the_launch(resume_harness):
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10", new_transcript=True)
    assert resume_harness["exec"] == ["claude"]
    assert resume_harness["env"] == "10"


def test_a_plain_resume_carries_no_mark(resume_harness, monkeypatch):
    monkeypatch.setenv(session_cmd.NEW_TRANSCRIPT_ENV, "10")
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")
    assert resume_harness["env"] is None


def test_goto_resume_new_transcript_marks_the_new_window(resume_harness):
    session_cmd._resume_new_window_pane("E-10", new_transcript=True)
    window = resume_harness["window"]
    assert window[-1] == "/bin/claude"
    i = window.index("-e")
    assert window[i + 1] == f"{session_cmd.NEW_TRANSCRIPT_ENV}=10"


def test_goto_resume_without_new_transcript_carries_no_mark(resume_harness):
    session_cmd._resume_new_window_pane("E-10")
    assert "-e" not in resume_harness["window"]
