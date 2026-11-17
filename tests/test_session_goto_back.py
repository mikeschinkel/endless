"""Tests for `endless session goto` / `session back` (E-1681)."""

import pytest

from endless import session_cmd


class _Result:
    """Minimal stand-in for subprocess.CompletedProcess."""

    def __init__(self, returncode: int, stdout: str):
        self.returncode = returncode
        self.stdout = stdout


# The fake task-session server's socket (E-2519). `goto_env` reports it as
# Endless's running task-session server and, by default, puts the caller's
# `$TMUX` on it — the pane goto is typed in is one of its panes.
FAKE_SOCKET = "/tmp/tmux-test"


class FakeTmux:
    """In-memory tmux server for `_tmux_run`: tracks existing panes, global
    options (the back-stack lives here), the spawner marker, and records every
    switch-client and select-window target. It stands in for `_tmux_run`, the
    one place that addresses the task-session server by socket (E-2519), so
    the socket itself is pinned by `test_tmux_run_addresses_the_tasks_server`.
    """

    def __init__(self, panes, spawned_by="", client="cli", session="$0",
                 session_name="tasks", attached=True):
        self.panes = set(panes)
        self.session = session
        self.session_name = session_name
        self.attached = attached
        self.options: dict[str, str] = {}
        self.spawned_by = spawned_by
        self.client = client
        self.switched: list[str] = []
        self.selected: list[str] = []
        self.new_windows: list[list[str]] = []
        self._next_new_pane = 1

    def run(self, args, timeout=2.0):
        return _Result(*self._dispatch(list(args)))

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
            if fmt == "#{session_id}":
                # E-2125: the pane -> landing-session lookup. This fake server
                # holds one session; an unresolvable pane errors, as real
                # tmux's `-t` resolution does.
                return (0, self.session + "\n") if target in self.panes else (1, "")
            if fmt == "#{@endless_spawned_by}":
                return 0, self.spawned_by + "\n"
            if fmt == "#{session_name}":
                return (0, self.session_name + "\n") if target in self.panes else (1, "")
            return 0, "\n"
        if args[:1] == ["select-window"]:
            pane = args[2]
            if pane in self.panes:
                self.selected.append(pane)
                return 0, ""
            return 1, ""
        if args[:1] == ["select-pane"]:
            return (0, "") if args[2] in self.panes else (1, "")
        if args[:1] == ["list-clients"]:
            return 0, ("/dev/ttys009\n" if self.attached else "")
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
    # E-2106: the resume path lays its window out via `endless-go spawn-layout`.
    # FakeTmux stands in for tmux, not for the Go binary, so the builder is
    # stubbed out — pane geometry has its own tests.
    monkeypatch.setattr(session_cmd, "build_pane_layout", lambda pane, cwd: None)

    def _make(panes, spawned_by="", current_pane=None, client="cli",
              session="$0", here=True, attached=True):
        """`here=False` types the command OUTSIDE the task-session server: in
        the console server (E-2519). `attached=False` leaves no terminal on the
        task-session server."""
        from endless import tmux_cmd
        if here:
            monkeypatch.setenv("TMUX", f"{FAKE_SOCKET},1,0")
        else:
            monkeypatch.setenv("TMUX", "/tmp/endless-501/endless-console,2,0")
        ft = FakeTmux(panes, spawned_by=spawned_by, client=client,
                      session=session, attached=attached)
        monkeypatch.setattr(session_cmd, "_tmux_run", ft.run)
        monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: (FAKE_SOCKET, "tasks"))
        monkeypatch.setattr(tmux_cmd, "session_exists", lambda socket, name: True)
        session_cmd._tasks_server_cache.clear()
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


# ─── from outside the task-session server (E-2519) ───────────────────────────
#
# Every session pane is on Endless's task-session server, and goto addresses
# that server by socket from wherever it is typed. Until E-2519 it ran bare
# `tmux` against the caller's own server, so from the console server every
# task pane read as unreachable — and `--resume` relaunched a running session.


def test_goto_from_the_console_selects_the_window_on_the_tasks_server(
    goto_env, capsys,
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10"}, current_pane="%3", here=False)

    session_cmd.session_goto("E-1465")

    assert ft.selected == ["%10"], "the window is selected on the tasks server"
    assert ft.switched == [], "no client of the console's is switched"
    assert ft.stack() == [], "nothing on that server to come back to"
    err = capsys.readouterr().err
    assert "goto E-1465" in err
    assert "shown in the terminal attached" in err


def test_goto_from_outside_with_no_terminal_attached_says_how_to_attach(
    goto_env, capsys,
):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10"}, here=False, attached=False)
    # Also outside tmux altogether: a plain shell.
    import os
    os.environ.pop("TMUX", None)

    session_cmd.session_goto("E-1465")

    assert ft.selected == ["%10"]
    err = capsys.readouterr().err
    assert "no terminal is attached" in err
    assert f"tmux -S {FAKE_SOCKET} attach -t '=tasks'" in err


@pytest.mark.parametrize("here", [True, False])
def test_tmux_run_addresses_the_tasks_server(monkeypatch, here):
    """`_tmux_run` is the one place tmux is invoked for a session pane, and it
    names the task-session server's socket on every call (E-2519). The
    caller's own $TMUX / $TMUX_PANE ride along only when the caller is in that
    server: a pane id means nothing on another server."""
    import subprocess
    from endless import tmux_cmd
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: (FAKE_SOCKET, "tasks"))
    session_cmd._tasks_server_cache.clear()
    monkeypatch.setenv("TMUX", f"{FAKE_SOCKET if here else '/tmp/other'},1,0")
    monkeypatch.setenv("TMUX_PANE", "%3")
    calls = []

    def fake_run(argv, **kw):
        calls.append((argv, kw["env"]))
        return _Result(0, "")
    monkeypatch.setattr(subprocess, "run", fake_run)

    session_cmd._tmux_run(["display-message", "-p", "#{pane_id}"])

    argv, env = calls[0]
    assert argv[:3] == ["tmux", "-S", FAKE_SOCKET]
    assert argv[3:] == ["display-message", "-p", "#{pane_id}"]
    assert ("TMUX_PANE" in env) is here
    assert ("TMUX" in env) is here


@pytest.mark.parametrize("here", [True, False])
def test_companion_resolvers_send_the_callers_pane_only_from_the_tasks_server(
        monkeypatch, here):
    """`_tmux_window_pane_ids`, `_window_claimed_task` and
    `_live_window_claiming` name the caller's pane with `-t` only when the
    caller is in the task-session server (E-2644). Pane ids are unique per
    server (E-2196): from the console, `%3` is a different pane there, so each
    answers as from outside tmux and never sends it."""
    import subprocess
    from endless import tmux_cmd
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: (FAKE_SOCKET, "tasks"))
    session_cmd._tasks_server_cache.clear()
    monkeypatch.setenv("TMUX", f"{FAKE_SOCKET if here else '/tmp/other'},1,0")
    monkeypatch.setenv("TMUX_PANE", "%3")
    monkeypatch.setattr(session_cmd, "_live_sessions",
                        lambda **kw: [{"task_id": 7, "pane_id": "%9",
                                       "endless_session_id": 70}])
    targets = []

    def fake_run(argv, **kw):
        if "-t" in argv:
            targets.append(argv[argv.index("-t") + 1])
        if "list-panes" in argv:
            return _Result(0, "%3\n%4\n")
        if "#{@endless_task_id}" in argv:
            return _Result(0, "7\n")
        return _Result(0, "@1 tasks:1 E-7\n")
    monkeypatch.setattr(subprocess, "run", fake_run)

    panes = session_cmd._tmux_window_pane_ids()
    claimed = session_cmd._window_claimed_task()
    other = session_cmd._live_window_claiming(7)

    assert ("%3" in targets) is here
    if here:
        assert panes == ["%3", "%4"]
        assert claimed == 7
        assert other is None  # %9 is in this very window
    else:
        assert panes is None
        assert claimed is None
        assert other == ("tasks:1 (E-7)", 70)


def test_tmux_run_answers_nothing_while_endless_is_not_running(monkeypatch):
    import subprocess
    from endless import tmux_cmd
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: None)
    session_cmd._tasks_server_cache.clear()
    monkeypatch.setattr(subprocess, "run",
                        lambda *a, **k: pytest.fail("tmux was invoked"))

    assert session_cmd._tmux_run(["list-panes"]) is None


def test_goto_refuses_while_endless_is_not_running(goto_env, monkeypatch, capsys):
    from endless import tmux_cmd
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10"}, current_pane="%3")
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: None)
    session_cmd._tasks_server_cache.clear()

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_goto("E-1465")

    assert exc.value.code == 1
    assert ft.selected == [] and ft.switched == []
    assert "Endless isn't running" in capsys.readouterr().err


# ─── --resume (E-1797) ────────────────────────────────────────────────────────


def _stage_transcript(monkeypatch, tmp_path, uuid):
    """Put `uuid`'s transcript on disk under a private Claude home.

    Since E-2106 a resume refuses a target whose transcript is gone, so "this
    resolves cleanly" includes the file being there. Isolated via
    CLAUDE_CONFIG_DIR so the test never reads (or depends on) the real one.
    """
    projects = tmp_path / "claude-home" / "projects" / "-staged"
    projects.mkdir(parents=True, exist_ok=True)
    (projects / f"{uuid}.jsonl").write_text("{}\n")
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", str(tmp_path / "claude-home"))


def _stage_resumable(monkeypatch, worktree, uuid="uuid-abc123",
                     eid=1748, task=1748, tmp_path=None):
    """Patch _resume_target + _require_claude so a resume resolves cleanly to a
    real on-disk worktree, without needing endless-go or a live `claude`."""
    monkeypatch.setattr(session_cmd, "_resume_target", lambda ref: {
        "endless_id": eid, "session_id": uuid, "task_id": task,
        "worktree_path": str(worktree), "state": "ended",
    })
    monkeypatch.setattr(session_cmd, "_require_claude", lambda: "/usr/bin/claude")
    _stage_transcript(monkeypatch, tmp_path or worktree, uuid)


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
    """A live target with a pane here is focused; `--resume` adds nothing. The
    claim and spawn refusals name `goto <id> --resume` whether the holder is
    running or ended, and this is the running half of that contract. A target
    running where this server cannot see it is the E-2519 refusal, tested in
    test_session_resume_refuses_running.py."""
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


def test_back_from_the_console_ignores_its_own_pane(goto_env, capsys):
    """From the console, the caller's `%cur` is a pane of ANOTHER server: it is
    never read for a spawner, and goto never pushes it as a token (E-2644)."""
    stage, make = goto_env
    stage(endless_session_id=207, pane_id="%207")
    ft = make({"%207", "%cur"}, spawned_by="207", current_pane="%cur",
              here=False)

    with pytest.raises(SystemExit):
        session_cmd.session_back()
    assert ft.switched == [] and ft.selected == []
    assert "no previous session" in capsys.readouterr().err
    assert session_cmd._current_pane_token(
        [{"pane_id": "%cur", "endless_session_id": 9}]) is None


def test_back_drops_stale_token_then_pops_next(goto_env):
    stage, make = goto_env
    ft = make({"%live", "%cur"}, current_pane="%cur")
    # Top of stack (%dead) is gone; the next (%live) is reachable.
    ft.options[session_cmd._backstack_key()] = "%live %dead"

    session_cmd.session_back()

    assert ft.switched == ["%live"]
    assert ft.stack() == []  # both consumed


def test_back_from_the_console_selects_the_window_on_the_tasks_server(
    goto_env, capsys,
):
    """`back` addresses the task-session server as goto does (E-2519): its
    stack lives there, and the pane it returns to is selected there."""
    stage, make = goto_env
    ft = make({"%10", "%20"}, here=False)
    # Keyed by the client tmux picks on THAT server (the terminal attached
    # there), since the console's own client is not one of its clients.
    ft.options["@endless_backstack_" + ft.client] = "%20"

    session_cmd.session_back()

    assert ft.selected == ["%20"]
    assert ft.switched == []
    assert "back → pane %20" in capsys.readouterr().err


def test_back_refuses_while_endless_is_not_running(goto_env, monkeypatch, capsys):
    from endless import tmux_cmd
    stage, make = goto_env
    make({"%10"}, current_pane="%10")
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: None)
    session_cmd._tasks_server_cache.clear()

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_back()
    assert exc.value.code == 1
    assert "Endless isn't running" in capsys.readouterr().err


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
    _stage_transcript(monkeypatch, worktree, "uuid-settled")
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

    # The revival route is a human_remedy (E-2159): it undoes the decision this
    # refusal protects, so it renders for a person and not for an agent. That
    # makes `format_message()` — what a reader is actually shown — the thing to
    # assert on, rather than the exception's bare message.
    msg = exc.value.format_message()
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


# ── E-2125: a new window lands in the session that asked for it ──

def test_goto_resume_targets_the_spawning_session(
    goto_env, registered_project, monkeypatch
):
    """An untargeted `new-window` lands in whichever session tmux considers
    current — the most recently active one on the server, which off a command
    line is whatever the operator was last looking at. So the window carries
    the target explicitly, and the target is the session holding the pane the
    command was run FROM.

    The fake's session id is deliberately not `$0`: a hard-coded default would
    pass this test without the pane ever being consulted.
    """
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10", "%cur"}, current_pane="%cur", session="$7")
    _stage_resumable(monkeypatch, registered_project)

    session_cmd.session_goto("E-1748", resume=True)

    argv = ft.new_windows[0]
    assert "-t" in argv, f"new-window has no target: {argv}"
    assert argv[argv.index("-t") + 1] == "$7:"


@pytest.mark.parametrize("here", [True, False])
def test_goto_resume_without_a_pane_of_its_own_targets_the_tasks_session(
    goto_env, registered_project, monkeypatch, here,
):
    """With no pane of the caller's on the task-session server — no $TMUX_PANE
    there, or a caller in the console (E-2519) — the window opens in that
    server's tasks session, named exactly, never in an untargeted new-window
    and never on the caller's own server."""
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", task_id=1465)
    ft = make({"%10"}, current_pane=None if here else "%3", here=here)
    _stage_resumable(monkeypatch, registered_project)

    session_cmd.session_goto("E-1748", resume=True)

    argv = ft.new_windows[0]
    assert argv[argv.index("-t") + 1] == "=tasks:"
    if here:
        assert ft.switched == ["%new1"]
    else:
        assert ft.selected == ["%new1"] and ft.switched == []
