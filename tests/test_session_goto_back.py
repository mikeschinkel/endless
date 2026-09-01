"""Tests for `endless session goto` / `session back` (E-1681)."""

import pytest

from endless import session_cmd


class _Result:
    """Minimal stand-in for subprocess.CompletedProcess."""

    def __init__(self, returncode: int, stdout: str):
        self.returncode = returncode
        self.stdout = stdout


class FakeTmux:
    """In-memory tmux server for `_tmux_run`: tracks existing panes, global
    options (the back-stack lives here), the spawner marker, and records every
    switch-client target.
    """

    def __init__(self, panes, spawned_by="", client="cli"):
        self.panes = set(panes)
        self.options: dict[str, str] = {}
        self.spawned_by = spawned_by
        self.client = client
        self.switched: list[str] = []
        self.new_windows: list[list[str]] = []
        self._next_new_pane = 1

    def run(self, args, timeout=2.0):
        return _Result(*self._dispatch(args))

    def _dispatch(self, args):
        if args[:1] == ["new-window"]:
            # Record the full argv and mint a fresh pane id, mirroring
            # `new-window -P -F '#{pane_id}'`. The new pane is now reachable.
            self.new_windows.append(args)
            pane = f"%new{self._next_new_pane}"
            self._next_new_pane += 1
            self.panes.add(pane)
            return 0, pane + "\n"
        if args[:2] == ["display-message", "-p"]:
            rest = args[2:]
            fmt = rest[-1]
            target = rest[1] if len(rest) >= 3 and rest[0] == "-t" else None
            if fmt == "#{client_name}":
                return 0, self.client + "\n"
            if fmt == "#{pane_id}":
                # Real tmux returns exit 0 with empty output for a bad -t pane.
                return (0, target + "\n") if target in self.panes else (0, "")
            if fmt == "#{@endless_spawned_by}":
                return 0, self.spawned_by + "\n"
            return 0, "\n"
        if args[:1] == ["show-options"]:
            return 0, self.options.get(args[-1], "") + "\n"
        if args[:1] == ["set-option"]:
            if args[1] == "-gu":  # unset a global option
                self.options.pop(args[2], None)
                return 0, ""
            self.options[args[2]] = args[3]
            return 0, ""
        if args[:1] == ["switch-client"]:
            pane = args[2]
            if pane in self.panes:
                self.switched.append(pane)
                return 0, ""
            return 1, ""
        return 0, ""

    def stack(self):
        """The current back-stack tokens (bottom-to-top)."""
        return self.options.get("@endless_backstack_" + self.client, "").split()


@pytest.fixture
def goto_env(registered_project, monkeypatch, stage_live_session):
    """Registered project as cwd + stage_live_session + a fake-tmux factory.

    The factory installs FakeTmux as `session_cmd._tmux_run`, marks the env as
    inside tmux, and optionally sets the current pane.
    """
    monkeypatch.chdir(registered_project)

    def _make(panes, spawned_by="", current_pane=None, client="cli"):
        monkeypatch.setenv("TMUX", "/tmp/tmux-test,1,0")
        ft = FakeTmux(panes, spawned_by=spawned_by, client=client)
        monkeypatch.setattr(session_cmd, "_tmux_run", ft.run)
        if current_pane is not None:
            monkeypatch.setenv("TMUX_PANE", current_pane)
        else:
            monkeypatch.delenv("TMUX_PANE", raising=False)
        return ft

    return stage_live_session, _make


def test_goto_by_task_id(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%1"}, current_pane="%1")

    session_cmd.session_goto("E-1465")

    assert ft.switched == ["%10"]
    # Current pane %1 isn't a tracked session, so it's pushed as a raw token.
    assert ft.stack() == ["%1"]
    err = capsys.readouterr().err
    assert "goto E-1465 → session 10 (pane %10)" in err


def test_goto_sets_no_nav_marker(goto_env):
    """goto sets no tmux option beyond the back-stack. E-1682 had it stamp a
    one-shot @endless_nav_via marker for the navigation-trail recorder; E-2081
    removed the trail, and goto must not leave the marker behind for a hook that
    no longer reads it."""
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%1"}, current_pane="%1")

    session_cmd.session_goto("E-1465")

    assert ft.switched == ["%10"]
    assert "@endless_nav_via" not in ft.options


def test_goto_by_session_id(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=None)
    ft = make({"%10", "%2"}, current_pane="%2")

    session_cmd.session_goto("10")

    assert ft.switched == ["%10"]


def test_goto_by_uuid_prefix(goto_env):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10",
          harness_session_id="abc12345-0000-0000-0000-000000000000")
    ft = make({"%10", "%3"}, current_pane="%3")

    session_cmd.session_goto("abc12345")

    assert ft.switched == ["%10"]


def test_goto_pushes_session_token_when_source_is_tracked(goto_env):
    stage, make = goto_env
    stage(endless_session_id=5, pane_id="%5")  # the source pane's session
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%5", "%10"}, current_pane="%5")

    session_cmd.session_goto("E-1465")

    assert ft.switched == ["%10"]
    # Source is session 5 → pushed as the session id, not the raw pane.
    assert ft.stack() == ["5"]


def test_goto_ambiguous_bare_number(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=5, pane_id="%5")               # session 5
    stage(endless_session_id=9, pane_id="%9", task_id=5)  # task 5
    ft = make({"%5", "%9", "%cur"}, current_pane="%cur")

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("5")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "ambiguous" in err.lower()
    assert "E-5" in err
    assert ft.switched == []
    assert ft.stack() == []  # nothing pushed on a failed resolution


def test_goto_no_live_session_for_task(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=999)
    ft = make({"%10", "%cur"}, current_pane="%cur")

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-1465")
    assert exc.value.code == 1
    assert "No live session on E-1465" in capsys.readouterr().err
    assert ft.switched == []


def test_goto_unknown_ref(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10")
    ft = make({"%10", "%cur"}, current_pane="%cur")

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("777")
    assert exc.value.code == 1
    assert "No live session matches '777'" in capsys.readouterr().err
    assert ft.switched == []


def test_goto_outside_tmux(goto_env, monkeypatch, capsys):
    stage, make = goto_env
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-1465")
    assert exc.value.code == 1
    assert "requires tmux" in capsys.readouterr().err


# ─── --resume (E-1797) ────────────────────────────────────────────────────────


def _stage_resumable(monkeypatch, worktree, uuid="uuid-abc123",
                     eid=1748, task=1748):
    """Patch _resume_target + _require_claude so a resume resolves cleanly to a
    real on-disk worktree, without needing endless-go or a live `claude`."""
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: {
        "endless_id": eid, "session_id": uuid, "task_id": task,
        "worktree_path": str(worktree), "state": "ended",
    })
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/usr/bin/claude")


def test_goto_resume_opens_new_window_when_not_live(
    goto_env, registered_project, monkeypatch, capsys
):
    stage, make = goto_env
    # A live session on a DIFFERENT task, so E-1748 has no live pane.
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project)

    session_cmd.session_goto("E-1748", resume=True)

    # One detached new window, in the worktree, running `claude --resume <uuid>`.
    assert len(ft.new_windows) == 1
    argv = ft.new_windows[0]
    assert "-d" in argv
    assert str(registered_project) in argv
    assert argv[-1] == "/usr/bin/claude --resume uuid-abc123"
    # …and focus switched to the freshly minted pane.
    assert ft.switched == ["%new1"]
    assert "goto --resume" in capsys.readouterr().err


def test_goto_resume_names_the_window_for_the_task(
    goto_env, registered_project, monkeypatch
):
    """E-2102: the resumed window is named `E-NNNN` and nothing else.

    Before, this call passed no `-n`, so tmux fell back to naming the window
    after its command and every resumed window on screen read `claude`. The id
    comes from the resolved target, not from the worktree path, because the
    target may carry a task that was minted during resolution.

    `internal/sandboxcmd/reapguard.go` reads this name back to decide which DB
    sandboxes to spare, so it is an interface, not a label.
    """
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project)

    session_cmd.session_goto("E-1748", resume=True)

    argv = ft.new_windows[0]
    assert "-n" in argv, f"the window must be named: {argv}"
    assert argv[argv.index("-n") + 1] == "E-1748"


def test_goto_resume_noop_when_live(goto_env, monkeypatch):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")

    def _no_resume(ref):
        pytest.fail("resume attempted on a live target")

    monkeypatch.setattr(session_cmd, "_resume_target", _no_resume)

    session_cmd.session_goto("E-1465", resume=True)

    assert ft.new_windows == []          # live → plain goto, no window spawned
    assert ft.switched == ["%10"]


def test_goto_not_live_error_names_resume_when_resumable(
    goto_env, registered_project, monkeypatch, capsys
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    make({"%10", "%cur"}, current_pane="%cur")
    _stage_resumable(monkeypatch, registered_project)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-1748")   # no --resume
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No live session on E-1748" in err
    assert "--resume" in err


def test_goto_not_live_error_keeps_list_hint_when_unknown(
    goto_env, monkeypatch, capsys
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    make({"%10", "%cur"}, current_pane="%cur")

    def _unknown(ref):
        raise RuntimeError("no such session/task")

    monkeypatch.setattr(session_cmd, "_resume_target", _unknown)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-9999")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No live session on E-9999" in err
    assert "session list" in err
    assert "--resume" not in err


def test_try_resume_target_reports_known_vs_unknown(monkeypatch):
    """The resumable-vs-unknown classifier `_fail_not_live` relies on: a resolved
    target → dict; any resolution failure → None (never raises)."""
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: {"endless_id": 1})
    assert session_cmd._try_resume_target("E-1") == {"endless_id": 1}

    def _boom(ref):
        raise RuntimeError("unknown")

    monkeypatch.setattr(session_cmd, "_resume_target", _boom)
    assert session_cmd._try_resume_target("E-2") is None


def test_back_pops_pushed_raw_pane(goto_env):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%1"}, current_pane="%1")

    session_cmd.session_goto("E-1465")   # pushes "%1", switches to %10
    session_cmd.session_back()           # pops "%1", switches back

    assert ft.switched == ["%10", "%1"]
    assert ft.stack() == []


def test_back_resolves_session_token_to_current_pane(goto_env):
    """A session-id token resolves to the session's CURRENT pane, so a session
    that moved to a new pane still works (the spawner-restart case)."""
    stage, make = goto_env
    src = stage(endless_session_id=5, pane_id="%5")
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%5", "%10"}, current_pane="%5")

    session_cmd.session_goto("E-1465")   # pushes session token "5"
    assert ft.stack() == ["5"]

    # Session 5 moves to a new pane; the old one is gone.
    src["pane_id"] = "%5b"
    ft.panes = {"%5b", "%10"}

    session_cmd.session_back()
    assert ft.switched[-1] == "%5b"
    assert ft.stack() == []


def test_back_empty_stack_falls_back_to_spawner(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=207, pane_id="%207")  # the spawner
    ft = make({"%207", "%cur"}, spawned_by="207", current_pane="%cur")

    session_cmd.session_back()

    assert ft.switched == ["%207"]
    assert "spawning session 207" in capsys.readouterr().err


def test_back_empty_stack_no_spawner(goto_env, capsys):
    stage, make = goto_env
    ft = make({"%cur"}, spawned_by="", current_pane="%cur")

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_back()
    assert exc.value.code == 1
    assert "no previous session" in capsys.readouterr().err
    assert ft.switched == []


def test_back_drops_stale_token_then_pops_next(goto_env):
    stage, make = goto_env
    ft = make({"%live", "%cur"}, current_pane="%cur")
    # Top of stack (%dead) is gone; the next (%live) is reachable.
    ft.options[session_cmd._backstack_key()] = "%live %dead"

    session_cmd.session_back()

    assert ft.switched == ["%live"]
    assert ft.stack() == []  # both consumed


def test_back_outside_tmux(goto_env, monkeypatch, capsys):
    stage, make = goto_env
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_back()
    assert exc.value.code == 1
    assert "requires tmux" in capsys.readouterr().err


# ─── --revisit / --no-revisit (E-1968) ────────────────────────────────────────
#
# `session goto <ref> --resume` on settled work is ambiguous: picking the work
# back up and reading it back mean opposite things for the task's status. The
# flags make the caller say which. They are the route `task spawn --reopen` was
# retired in favour of, so they carry that capability's weight.


def _stage_settled(monkeypatch, worktree, status, task=1748, eid=1748):
    """A resumable target whose task carries `status`, with the status-change
    emitter captured so a test can assert on the transition (or its absence)."""
    emitted: list[tuple] = []
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: {
        "endless_id": eid, "session_id": "uuid-settled", "task_id": task,
        "worktree_path": str(worktree), "state": "ended",
        "task_status": status, "task_title": "settled task",
    })
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/usr/bin/claude")
    monkeypatch.setattr(
        session_cmd, "_emit_task_status_change",
        lambda *a, **kw: emitted.append((a, kw)),
    )
    return emitted


@pytest.mark.parametrize("status", ["confirmed", "assumed", "completed"])
def test_goto_resume_settled_requires_an_explicit_intent(
    goto_env, registered_project, monkeypatch, status,
):
    """Neither flag → refuse, naming both and what each does."""
    import click

    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    emitted = _stage_settled(monkeypatch, registered_project, status)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.session_goto("E-1748", resume=True)

    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "--revisit" in msg and "--no-revisit" in msg
    # A refusal opens no window and changes no status.
    assert ft.new_windows == []
    assert emitted == []


def test_goto_resume_revisit_flips_status_and_opens(
    goto_env, registered_project, monkeypatch,
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    emitted = _stage_settled(monkeypatch, registered_project, "assumed")

    session_cmd.session_goto("E-1748", resume=True, revisit=True)

    assert len(emitted) == 1
    args, kwargs = emitted[0]
    assert args[0] == 1748              # task id
    assert args[2] == "assumed"         # from
    assert args[3] == "revisit"         # to
    # Attributed to the RESUMED session, not the pane running the command.
    assert kwargs["session_id"] == 1748
    assert len(ft.new_windows) == 1
    assert ft.switched == ["%new1"]


def test_goto_resume_no_revisit_opens_without_touching_status(
    goto_env, registered_project, monkeypatch,
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    emitted = _stage_settled(monkeypatch, registered_project, "confirmed")

    session_cmd.session_goto("E-1748", resume=True, no_revisit=True)

    assert emitted == []
    assert len(ft.new_windows) == 1
    assert ft.switched == ["%new1"]


@pytest.mark.parametrize("status", ["underway", "unverified", "revisit",
                                    "ready", "unplanned"])
def test_goto_resume_unsettled_needs_no_flag(
    goto_env, registered_project, monkeypatch, status,
):
    """Nothing about an in-flight or not-yet-started task is ambiguous, so the
    gate must not fire on it — the flags would be friction with no question."""
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    emitted = _stage_settled(monkeypatch, registered_project, status)

    session_cmd.session_goto("E-1748", resume=True)

    assert emitted == []
    assert len(ft.new_windows) == 1


@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_goto_resume_revisit_refuses_a_decision(
    goto_env, registered_project, monkeypatch, status,
):
    """Matches `session resume --reopen`: reviving a deliberate decision not to
    do the work is an explicit act, not a navigation side effect."""
    import click

    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur")
    emitted = _stage_settled(monkeypatch, registered_project, status)

    with pytest.raises(click.ClickException) as exc:
        session_cmd.session_goto("E-1748", resume=True, revisit=True)

    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "task update E-1748 --status revisit" in msg
    assert ft.new_windows == []
    assert emitted == []


def test_goto_resume_revisit_and_no_revisit_are_mutually_exclusive(
    goto_env, registered_project, monkeypatch,
):
    import click

    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    make({"%10", "%cur"}, current_pane="%cur")
    _stage_settled(monkeypatch, registered_project, "assumed")

    with pytest.raises(click.ClickException) as exc:
        session_cmd.session_goto(
            "E-1748", resume=True, revisit=True, no_revisit=True,
        )
    assert "mutually exclusive" in str(exc.value)


@pytest.mark.parametrize("kwargs", [{"revisit": True}, {"no_revisit": True}])
def test_revisit_flags_require_resume(goto_env, kwargs):
    """Plain goto is a focus change; a live target's status is its own session's
    business, so the flags must not silently do nothing."""
    import click

    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    make({"%10", "%cur"}, current_pane="%cur")

    with pytest.raises(click.ClickException) as exc:
        session_cmd.session_goto("E-1465", **kwargs)
    assert "only with --resume" in str(exc.value)
