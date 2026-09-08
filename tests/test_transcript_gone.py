"""A resume whose Claude transcript is gone is refused, and routed (E-2106).

Endless resolved a resume target out of `sessions.session_id` and handed the
UUID to `claude --resume` without ever checking the transcript was still there,
so the loss dead-ended differently at each entry point: `session goto --resume`
appeared to do nothing (the window really was created, so tmux reported success
— then the claude inside exited and took the window down with it), and
`session resume` surfaced only Claude's own error. Neither named a route back.

These pin the refusal on both verbs, the glob that locates a transcript filed
under a slug the worktree cannot predict, the `--new-transcript` give-up route,
and the window shape both verbs now build.
"""

import click
import pytest

from endless import session_cmd


class _Exec(Exception):
    """Raised by the stub execvp so a test can stop where the real one would."""


class _Res:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _target(**over):
    base = {
        "endless_id": 99,
        "session_id": "dead-beef-uuid",
        "task_id": 10,
        "project_id": 3,
        "worktree_path": "",
        "state": "ended",
        "task_type": "todo",
        "task_status": "underway",
        "task_title": "Resume me",
        "landed_sha": "",
    }
    base.update(over)
    return base


@pytest.fixture
def claude_home(monkeypatch, tmp_path):
    """An empty Claude home. Yields its `projects/` dir so a test can fill it."""
    projects = tmp_path / "claude-home" / "projects"
    projects.mkdir(parents=True)
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", str(tmp_path / "claude-home"))
    return projects


@pytest.fixture
def harness(monkeypatch, tmp_path, claude_home):
    """Drive both resume verbs with tmux, claude and the layout builder stubbed.

    Yields the ordered trace of what the command did to the outside world, so a
    test can assert both content and ordering: `("tmux", [...])` per tmux
    invocation, `("layout", pane)` per layout build, `("exec", argv)` for the
    exec that replaces the process.
    """
    wt = tmp_path / "wt"
    wt.mkdir()
    trace: list[tuple] = []

    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    monkeypatch.setenv("TMUX_PANE", "%42")
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _target(worktree_path=str(wt))
    )
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%42"])
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)
    monkeypatch.setattr(
        session_cmd, "build_pane_layout",
        lambda pane, cwd: trace.append(("layout", pane)),
    )

    def fake_tmux(args, timeout=2.0):
        trace.append(("tmux", list(args)))
        if args and args[0] == "new-window":
            return _Res(stdout="%77\n")
        return _Res()

    def fake_exec(file, argv):
        trace.append(("exec", list(argv)))
        raise _Exec()

    monkeypatch.setattr(session_cmd, "_tmux_run", fake_tmux)
    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    return trace


def _kinds(trace, kind):
    return [payload for k, payload in trace if k == kind]


def _windows(trace):
    return [a for a in _kinds(trace, "tmux") if a and a[0] == "new-window"]


# ── Locating a transcript ────────────────────────────────────────────────────

def test_transcript_is_found_under_any_project_slug(claude_home):
    """The slug encodes where the session ENDED, not where it started, so a
    session that ran `/cd` is filed somewhere the worktree cannot predict.
    Deriving the slug reported a confident, false 'missing' for exactly that
    case; globbing every project directory finds it."""
    elsewhere = claude_home / "-Users-someone-somewhere-else"
    elsewhere.mkdir()
    (elsewhere / "dead-beef-uuid.jsonl").write_text("{}\n")

    found = session_cmd.transcript_path("dead-beef-uuid")

    assert found is not None
    assert found.name == "dead-beef-uuid.jsonl"


def test_missing_transcript_reads_as_missing(claude_home):
    assert session_cmd.transcript_path("dead-beef-uuid") is None


def test_no_uuid_is_not_a_transcript(claude_home):
    assert session_cmd.transcript_path("") is None


def test_claude_config_dir_overrides_the_home(monkeypatch, tmp_path):
    """PRODUCT: not everyone's Claude home is ~/.claude, and CLAUDE_CONFIG_DIR
    is Claude Code's own override for saying so."""
    elsewhere = tmp_path / "xdg" / "claude"
    (elsewhere / "projects").mkdir(parents=True)
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", str(elsewhere))

    assert session_cmd.claude_projects_dir() == elsewhere / "projects"


# ── Both verbs refuse ────────────────────────────────────────────────────────

def test_resume_refuses_and_launches_nothing(harness):
    with pytest.raises(click.ClickException):
        session_cmd.resume_session("E-10")

    assert _kinds(harness, "exec") == []


def test_goto_resume_creates_no_window(harness):
    """The defect this task was filed for: `tmux new-window` succeeds, so the
    caller sees `(new window)` and a pane id, while the claude inside exits on
    the missing transcript and takes the window down with it."""
    with pytest.raises(click.ClickException):
        session_cmd._resume_new_window_pane("E-10")

    assert _windows(harness) == []


def test_the_refusal_names_the_uuid_where_it_looked_and_the_route(harness):
    with pytest.raises(click.ClickException) as err:
        session_cmd.resume_session("E-10")

    message = err.value.format_message()
    assert "dead-beef-uuid.jsonl" in message
    assert "projects" in message
    assert "backup" in message
    assert "--new-transcript" in message


def test_a_present_transcript_still_resumes(harness, claude_home):
    home = claude_home / "-some-project"
    home.mkdir()
    (home / "dead-beef-uuid.jsonl").write_text("{}\n")

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    assert _kinds(harness, "exec") == [
        ["claude", "--resume", "dead-beef-uuid"]
    ]


# ── --new-transcript ─────────────────────────────────────────────────────────

def test_new_transcript_launches_a_plain_claude_in_place(harness):
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10", new_transcript=True)

    assert _kinds(harness, "exec") == [["claude"]]


def test_new_transcript_opens_a_plain_claude_in_a_new_window(harness):
    pane, label = session_cmd._resume_new_window_pane(
        "E-10", new_transcript=True
    )

    assert pane == "%77"
    assert "--new-transcript" in label
    window = _windows(harness)[0]
    assert window[-1] == "/bin/claude", "no --resume on a fresh transcript"


def test_new_transcript_does_not_republish_the_dead_uuid(harness):
    """The pane is about to hold a DIFFERENT session. Re-publishing the old
    uuid as the window's identity would point everything that reads the window
    — the clobber guard, `session status`, the hook's bind — at a session that
    is not there."""
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10", new_transcript=True)

    written = {
        a[4]: a[5] for a in _kinds(harness, "tmux")
        if a[:2] == ["set-option", "-w"]
    }
    assert written.get("@endless_task_id") == "10"
    assert "@endless_session_uuid" not in written


def test_new_transcript_needs_resume_on_goto():
    with pytest.raises(click.ClickException) as err:
        session_cmd.session_goto("E-10", new_transcript=True)
    assert "--resume" in err.value.format_message()


# ── Window shape ─────────────────────────────────────────────────────────────

def test_resume_refuses_a_populated_window(harness, claude_home, monkeypatch):
    home = claude_home / "-some-project"
    home.mkdir()
    (home / "dead-beef-uuid.jsonl").write_text("{}\n")
    monkeypatch.setattr(
        session_cmd, "_tmux_window_pane_ids", lambda: ["%42", "%43"]
    )

    with pytest.raises(click.ClickException) as err:
        session_cmd.resume_session("E-10")

    assert "goto E-10 --resume" in err.value.format_message()
    assert _kinds(harness, "exec") == []


def test_resume_outside_tmux_is_not_a_populated_window(harness, claude_home,
                                                      monkeypatch):
    """No window to crowd, nothing to lay out — and a resume must not become
    less usable than the crash it is recovering from."""
    home = claude_home / "-some-project"
    home.mkdir()
    (home / "dead-beef-uuid.jsonl").write_text("{}\n")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: None)

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")


def test_each_verb_builds_the_layout_exactly_once(harness, claude_home):
    home = claude_home / "-some-project"
    home.mkdir()
    (home / "dead-beef-uuid.jsonl").write_text("{}\n")

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")
    assert _kinds(harness, "layout") == ["%42"], "resume lays out its own pane"

    harness.clear()
    session_cmd._resume_new_window_pane("E-10")
    assert _kinds(harness, "layout") == ["%77"], "goto lays out the new pane"


def test_resume_lays_out_before_the_exec(harness, claude_home):
    """The exec replaces this process, so anything it was going to orchestrate
    afterward never happens."""
    home = claude_home / "-some-project"
    home.mkdir()
    (home / "dead-beef-uuid.jsonl").write_text("{}\n")

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    kinds = [k for k, _ in harness]
    assert kinds.index("layout") < kinds.index("exec")
