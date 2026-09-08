"""`session resume` re-binds the tmux window options on its pane (E-2104).

`session resume` execs `claude --resume` in the CURRENT pane, so after the exec
that pane holds a different session working a different task than the window's
`@endless_*` options say. Spawn publishes those options
(internal/spawnlaunchcmd/tmux_driver.go); resume never did, so a resumed pane
carried stale identity — or none at all — and everything that reads the window
(`_current_pane_task`'s clobber guard, `session status` focal resolution, the
hook's spawn-bind) read the wrong task.

These pin the re-bind: the options are set on `$TMUX_PANE`, from the resolved
target, and always BEFORE the exec that replaces this process.
"""

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
        "session_id": "uuid-xyz",
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
def harness(monkeypatch, tmp_path, stage_transcript):
    """Drive `resume_session` to the exec with tmux and claude stubbed.

    Yields the ordered trace of what the command did to the outside world:
    `("tmux", [...])` per tmux invocation and `("exec", argv)` for the exec, so
    a test can assert both the content and the ordering.
    """
    wt = tmp_path / "wt"
    wt.mkdir()
    trace: list[tuple] = []

    monkeypatch.setenv("TMUX_PANE", "%42")
    # E-2106: a resume refuses a target whose transcript is gone, and these
    # tests are about the window options a resume that PROCEEDS writes.
    stage_transcript("uuid-xyz")
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%42"])
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _target(worktree_path=str(wt))
    )
    monkeypatch.setattr(session_cmd, "_current_pane_task", lambda: None)
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd.os, "chdir", lambda p: None)

    def fake_tmux(args, timeout=2.0):
        trace.append(("tmux", list(args)))
        return _Res()

    def fake_exec(file, argv):
        trace.append(("exec", list(argv)))
        raise _Exec()

    monkeypatch.setattr(session_cmd, "_tmux_run", fake_tmux)
    monkeypatch.setattr(session_cmd.os, "execvp", fake_exec)
    return trace


def _options(trace) -> dict[str, str]:
    """The @endless_* window options the run set, as {key: value}."""
    out = {}
    for kind, args in trace:
        if kind == "tmux" and args[:2] == ["set-option", "-w"]:
            out[args[4]] = args[5]
    return out


def test_sets_the_window_identity_before_exec(harness):
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    assert _options(harness) == {
        "@endless_task_id": "10",
        "@endless_project_id": "3",
        "@endless_session_uuid": "uuid-xyz",
    }
    kinds = [k for k, _ in harness]
    assert kinds[-1] == "exec", "the exec must come after every option write"
    assert kinds.count("exec") == 1


def test_options_target_the_current_pane(harness):
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    for kind, args in harness:
        if kind == "tmux":
            assert args[2:4] == ["-t", "%42"]


def test_spawned_by_is_left_alone(harness):
    """Resume execs into an EXISTING window; it did not spawn one. The marker
    records who created the window, so resume neither sets nor clears it."""
    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    assert "@endless_spawned_by" not in _options(harness)


def test_no_tmux_no_options(harness, monkeypatch):
    """Recovery from a bare shell outside tmux still resumes."""
    monkeypatch.delenv("TMUX_PANE")

    with pytest.raises(_Exec):
        session_cmd.resume_session("E-10")

    assert [k for k, _ in harness] == ["exec"]


def test_dry_run_touches_nothing(harness, capsys):
    """--dry-run stops short of the exec, so it must not rewrite the pane it
    is not going to replace."""
    session_cmd.resume_session("E-10", dry_run=True)

    assert harness == []
    capsys.readouterr()


# ── `session goto --resume` opens a NEW window, which starts with no identity ──

def test_new_window_gets_the_same_identity(monkeypatch, tmp_path,
                                           stage_transcript):
    wt = tmp_path / "wt"
    wt.mkdir()
    stage_transcript("uuid-xyz")
    trace: list[list[str]] = []

    def fake_tmux(args, timeout=2.0):
        trace.append(list(args))
        if args[:1] == ["new-window"]:
            return _Res(stdout="%new1\n")
        if args[-1] == "#{session_id}":
            return _Res(stdout="$0\n")
        return _Res()

    monkeypatch.setenv("TMUX_PANE", "%42")
    monkeypatch.setattr(
        session_cmd, "_resume_target", lambda ref: _target(worktree_path=str(wt))
    )
    monkeypatch.setattr(session_cmd, "_apply_revisit_intent", lambda *a: None)
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/bin/claude")
    monkeypatch.setattr(session_cmd, "_tmux_run", fake_tmux)
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)

    pane, _label = session_cmd._resume_new_window_pane("E-10")

    assert pane == "%new1"
    opts = {a[4]: a[5] for a in trace if a[:2] == ["set-option", "-w"]}
    assert opts == {
        "@endless_task_id": "10",
        "@endless_project_id": "3",
        "@endless_session_uuid": "uuid-xyz",
    }
    # On the window that was just created — never on the pane goto was run from.
    for args in trace:
        if args[:2] == ["set-option", "-w"]:
            assert args[2:4] == ["-t", "%new1"]
    window = next(i for i, a in enumerate(trace) if a[:1] == ["new-window"])
    first_opt = next(i for i, a in enumerate(trace) if a[:2] == ["set-option", "-w"])
    assert window < first_opt, "identity is written after the window"
    # E-2125: and the window says which session it lands in.
    argv = trace[window]
    assert "-t" in argv, f"new-window has no target: {argv}"
    assert argv[argv.index("-t") + 1] == "$0:"
