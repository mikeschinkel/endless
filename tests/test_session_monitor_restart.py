"""Tests for E-2194: `endless session monitor --restart`.

Python owns only the Click options and the pass-through; finding the tagged
panes, the stale-tag rule and the respawn live in Go and are tested there
(internal/sessionmonitorcmd, internal/upid). So these assert the boundary: each
flag reaches `endless-go session-monitor restart`, and the flags that only mean
something together are refused apart.
"""

import subprocess

import pytest
from click.testing import CliRunner

from endless import session_cmd
from endless.cli import main


@pytest.fixture
def runner():
    return CliRunner()


@pytest.fixture
def argv(monkeypatch):
    seen = {}

    def fake_run(args, *a, **k):
        seen["argv"] = args
        return subprocess.CompletedProcess(args, 0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr("shutil.which", lambda name: "/fake/" + name)
    return seen


@pytest.mark.parametrize("flags, forwarded", [
    ([], []),
    (["--tmux-session", "active"], ["--tmux-session", "active"]),
    (["--tmux-session=active"], ["--tmux-session", "active"]),
    (["--all-tmux-sessions"], ["--all-tmux-sessions"]),
    (["--dry-run"], ["--dry-run"]),
])
def test_restart_forwards_to_go(runner, argv, flags, forwarded):
    result = runner.invoke(main, ["session", "monitor", "--restart", *flags])
    assert result.exit_code == 0, result.output
    assert argv["argv"] == ["/fake/endless-go", "session-monitor", "restart", *forwarded]


@pytest.mark.parametrize("flags", [
    ["--tmux-session", "active"], ["--all-tmux-sessions"], ["--dry-run"],
])
def test_restart_only_flags_need_restart(runner, argv, flags):
    result = runner.invoke(main, ["session", "monitor", *flags])
    assert result.exit_code == 2
    assert "only applies with --restart" in result.output
    assert "argv" not in argv


@pytest.mark.parametrize("flag", ["--all", "--tree", "--show-hidden", "--only-hidden"])
def test_view_flags_refused_with_restart(runner, argv, flag):
    result = runner.invoke(main, ["session", "monitor", "--restart", flag])
    assert result.exit_code == 2
    assert "does not apply with --restart" in result.output
    assert "argv" not in argv


def test_both_scopes_refused(runner, argv):
    result = runner.invoke(main, ["session", "monitor", "--restart",
                                  "--tmux-session", "a", "--all-tmux-sessions"])
    assert result.exit_code == 2
    assert "mutually exclusive" in result.output
    assert "argv" not in argv


def test_go_failure_is_the_exit_code(runner, monkeypatch):
    monkeypatch.setattr(subprocess, "run",
                        lambda args, *a, **k: subprocess.CompletedProcess(args, 1))
    monkeypatch.setattr("shutil.which", lambda name: "/fake/" + name)
    result = runner.invoke(main, ["session", "monitor", "--restart"])
    assert result.exit_code == 1


def test_restart_is_not_a_sqlite_reader():
    # CLAUDE.md: no new Python SQLite access. The pass-through must not grow one.
    import inspect
    src = inspect.getsource(session_cmd.session_monitor_restart)
    assert "sqlite" not in src and "db." not in src
