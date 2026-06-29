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

    def run(self, args, timeout=2.0):
        return _Result(*self._dispatch(args))

    def _dispatch(self, args):
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
    stage(endless_session_id=10, pane_id="%10", active_task_id=1465)
    ft = make({"%10", "%1"}, current_pane="%1")

    session_cmd.session_goto("E-1465")

    assert ft.switched == ["%10"]
    # Current pane %1 isn't a tracked session, so it's pushed as a raw token.
    assert ft.stack() == ["%1"]
    err = capsys.readouterr().err
    assert "goto E-1465 → session 10 (pane %10)" in err


def test_goto_by_session_id(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", active_task_id=None)
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
    stage(endless_session_id=10, pane_id="%10", active_task_id=1465)
    ft = make({"%5", "%10"}, current_pane="%5")

    session_cmd.session_goto("E-1465")

    assert ft.switched == ["%10"]
    # Source is session 5 → pushed as the session id, not the raw pane.
    assert ft.stack() == ["5"]


def test_goto_ambiguous_bare_number(goto_env, capsys):
    stage, make = goto_env
    stage(endless_session_id=5, pane_id="%5")               # session 5
    stage(endless_session_id=9, pane_id="%9", active_task_id=5)  # task 5
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
    stage(endless_session_id=10, pane_id="%10", active_task_id=999)
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


def test_back_pops_pushed_raw_pane(goto_env):
    stage, make = goto_env
    stage(endless_session_id=10, pane_id="%10", active_task_id=1465)
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
    stage(endless_session_id=10, pane_id="%10", active_task_id=1465)
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
