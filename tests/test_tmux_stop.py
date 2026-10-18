"""`endless tmux stop` / `endless stop` (E-2385).

The stop itself is Go's (internal/tmuxstopcmd), proved there and against real
tmux servers by the task's verify suite. These pin the Python surface: the two
spellings are one command, a bare `stop` prints usage, and every flag reaches
endless-go.
"""

import subprocess
from types import SimpleNamespace

import pytest
from click.testing import CliRunner

from endless import cli, tmux_cmd


def test_stop_is_the_same_command_as_tmux_stop():
    assert cli.main.commands["stop"] is cli.tmux_cmd.commands["stop"]


@pytest.fixture
def go_calls(monkeypatch):
    calls = []
    monkeypatch.setattr(tmux_cmd, "_binary", lambda: "/bin/endless-go")
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: calls.append(cmd) or SimpleNamespace(returncode=0))
    return calls


def test_bare_stop_prints_usage_and_stops_nothing(go_calls):
    result = CliRunner().invoke(cli.main, ["stop"])
    assert result.exit_code == 2
    assert "Usage:" in result.output
    assert go_calls == []


@pytest.mark.parametrize("argv,want", [
    (["stop", "tasks"], ["tasks"]),
    (["stop", "console"], ["console"]),
    (["stop", "all"], ["all"]),
    (["stop", "--all"], ["--all"]),
    (["tmux", "stop", "tasks", "--force", "--timeout", "5s"],
     ["--force", "--timeout", "5s", "tasks"]),
])
def test_stop_shells_to_endless_go(go_calls, argv, want):
    result = CliRunner().invoke(cli.main, argv)
    assert result.exit_code == 0, result.output
    assert go_calls == [["/bin/endless-go", "tmux", "stop", *want]]


def test_an_unknown_target_is_refused(go_calls):
    result = CliRunner().invoke(cli.main, ["stop", "everything"])
    assert result.exit_code == 2
    assert go_calls == []
