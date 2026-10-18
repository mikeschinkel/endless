"""`endless tmux start` / `endless start` and the task-session server's reach
into the spawn and claim paths (E-2240).

The servers themselves are started by Go (internal/tmuxstartcmd) and proved
against real tmux servers by the task's verify suite. These pin the Python
surface: the command wiring, the required project name, and how the spawn
and claim paths read "is the task-session server running".
"""

import subprocess
from types import SimpleNamespace

import pytest
from click.testing import CliRunner

from endless import cli, tmux_cmd


def test_start_is_the_same_command_as_tmux_start():
    assert cli.main.commands["start"] is cli.tmux_cmd.commands["start"]


def test_start_shells_to_endless_go(monkeypatch):
    calls = []
    monkeypatch.setattr(tmux_cmd, "_binary", lambda: "/bin/endless-go")
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: calls.append(cmd) or SimpleNamespace(returncode=0))
    tmux_cmd.run_start(no_restore=False)
    tmux_cmd.run_start(no_restore=True)
    assert calls == [["/bin/endless-go", "tmux", "start"],
                     ["/bin/endless-go", "tmux", "start", "--no-restore"]]


def test_project_monitor_tmux_requires_a_name(monkeypatch):
    called = []
    from endless import project_status_cmd
    monkeypatch.setattr(project_status_cmd, "project_window_resolve",
                        lambda *a, **k: called.append(a))
    result = CliRunner().invoke(cli.main, ["project", "monitor", "--tmux"])
    assert result.exit_code == 2
    assert "needs the project NAME" in result.output
    assert called == []


@pytest.mark.tasks_server
def test_tasks_server_parses_the_target(monkeypatch):
    monkeypatch.setattr(tmux_cmd.shutil, "which", lambda name: "/bin/endless-go")
    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: SimpleNamespace(
        returncode=0, stdout="/tmp/endless-501/endless-tasks\ttasks\n"))
    assert tmux_cmd.tasks_server() == ("/tmp/endless-501/endless-tasks", "tasks")


@pytest.mark.tasks_server
def test_tasks_server_not_running(monkeypatch):
    monkeypatch.setattr(tmux_cmd.shutil, "which", lambda name: "/bin/endless-go")
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: SimpleNamespace(returncode=1, stdout=""))
    assert tmux_cmd.tasks_server() is None
    monkeypatch.setattr(tmux_cmd.shutil, "which", lambda name: None)
    assert tmux_cmd.tasks_server() is None


def test_reach_line_attaches_from_outside_and_quotes_for_zsh(monkeypatch):
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: ("/s/endless-tasks", "tasks"))
    monkeypatch.delenv("TMUX", raising=False)
    # zsh expands a bare `=word`, so the target is single-quoted.
    assert tmux_cmd.reach_window_line("E-5") == \
        "tmux -S /s/endless-tasks attach -t '=tasks:E-5'"
    monkeypatch.setenv("TMUX", "/s/endless-tasks,1,0")
    assert tmux_cmd.reach_window_line("E-5") == "tmux select-window -t '=tasks:E-5'"


def test_reach_line_is_none_without_the_server():
    assert tmux_cmd.reach_window_line("E-5") is None


def test_claim_from_a_plain_shell_needs_no_tmux_while_the_server_runs(monkeypatch):
    from endless import task_cmd
    monkeypatch.delenv("TMUX", raising=False)
    # Not running (the autouse stub): a shell outside tmux is refused, and
    # (E-2385) so is one inside another tmux server.
    with pytest.raises(Exception):
        task_cmd._require_tmux_for_claim(1)
    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1,0")
    with pytest.raises(Exception):
        task_cmd._require_tmux_for_claim(1)
    # Running: the session's window opens there, so the claim proceeds.
    monkeypatch.setattr(tmux_cmd, "tasks_server", lambda: ("/s/endless-tasks", "tasks"))
    task_cmd._require_tmux_for_claim(1)
