"""Tests for `endless task id` / `endless tmux task` (E-1302).

The session→task binding lives in the database and is read by
`endless-go tmux active-id`; the Python verb is the user-facing surface over
that read. What matters here is the *contract* of that surface, not the Go
lookup (which has its own tests in internal/monitor):

  - stdout carries exactly one bare `E-NNNN` line, so `$(endless task id)`
    captures something usable;
  - nothing but that reaches stdout — every diagnostic goes to stderr, so a
    failed capture is empty rather than a sentence;
  - the exit code is non-zero when there is no task;
  - the two spellings are the same command object, not two implementations.

The subprocess is faked so the suite never depends on a built binary, a
running tmux server, or a seeded session row.
"""

import subprocess

import pytest
from click.testing import CliRunner

from endless import tmux_cmd
from endless.cli import main


class _Result:
    """Stand-in for subprocess.CompletedProcess."""

    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


@pytest.fixture
def fake_go(monkeypatch):
    """Fake `endless-go`, recording the argv each invocation was given."""
    calls: list[list[str]] = []
    box: dict[str, _Result] = {"result": _Result(stdout="E-1302\n")}

    monkeypatch.setattr(tmux_cmd, "_binary", lambda: "/fake/endless-go")

    def fake_run(cmd, *args, **kwargs):
        calls.append(list(cmd))
        return box["result"]

    monkeypatch.setattr(tmux_cmd.subprocess, "run", fake_run)
    return {"calls": calls, "box": box}


# `endless task id` and `endless tmux task` must behave identically; every
# behavioural test below runs against both spellings.
SPELLINGS = [["task", "id"], ["tmux", "task"]]


@pytest.mark.parametrize("argv", SPELLINGS)
def test_prints_bare_task_id(fake_go, argv):
    runner = CliRunner()
    result = runner.invoke(main, argv)

    assert result.exit_code == 0
    assert result.stdout == "E-1302\n"


@pytest.mark.parametrize("argv", SPELLINGS)
def test_shells_to_go_active_id(fake_go, argv):
    CliRunner().invoke(main, argv)

    assert fake_go["calls"] == [["/fake/endless-go", "tmux", "active-id"]]


@pytest.mark.parametrize("argv", SPELLINGS)
def test_pane_is_forwarded(fake_go, argv):
    CliRunner().invoke(main, argv + ["--pane", "%42"])

    assert fake_go["calls"] == [
        ["/fake/endless-go", "tmux", "active-id", "--pane", "%42"]
    ]


@pytest.mark.parametrize("argv", SPELLINGS)
def test_no_task_exits_non_zero_with_empty_stdout(fake_go, monkeypatch, argv):
    """The scripting contract: a failed `$( )` capture is empty, not prose."""
    fake_go["box"]["result"] = _Result(returncode=1)
    monkeypatch.setenv("TMUX_PANE", "%42")

    runner = CliRunner()
    result = runner.invoke(main, argv, catch_exceptions=False)

    assert result.exit_code == 1
    assert result.stdout == ""
    assert "no active task" in result.stderr


@pytest.mark.parametrize("argv", SPELLINGS)
def test_outside_tmux_says_so(fake_go, monkeypatch, argv):
    """The two causes of 'no task' get two different messages."""
    fake_go["box"]["result"] = _Result(returncode=1)
    monkeypatch.delenv("TMUX_PANE", raising=False)

    result = CliRunner().invoke(main, argv, catch_exceptions=False)

    assert result.exit_code == 1
    assert "not inside tmux" in result.stderr
    assert "task claim" not in result.stderr


@pytest.mark.parametrize("argv", SPELLINGS)
def test_message_names_the_spelling_the_user_typed(fake_go, monkeypatch, argv):
    fake_go["box"]["result"] = _Result(returncode=1)
    monkeypatch.setenv("TMUX_PANE", "%42")

    result = CliRunner().invoke(main, argv, catch_exceptions=False)

    # The prefix is ctx.command_path, so under CliRunner the program name is
    # the callback's name ("main") rather than "endless". What this pins is
    # the part that differs between the two spellings: the message names the
    # subcommand path the user actually typed, not a hardcoded one.
    prefix = result.stderr.split(":", 1)[0]
    assert prefix.endswith(" ".join(argv))


@pytest.mark.parametrize("argv", SPELLINGS)
def test_real_go_error_is_relayed_not_overwritten(fake_go, monkeypatch, argv):
    """A DB failure explains itself; don't replace it with a guess about panes."""
    fake_go["box"]["result"] = _Result(
        returncode=1, stderr="endless-tmux active-id: database is locked\n"
    )
    monkeypatch.delenv("TMUX_PANE", raising=False)

    result = CliRunner().invoke(main, argv, catch_exceptions=False)

    assert result.exit_code == 1
    assert "database is locked" in result.stderr
    assert "not inside tmux" not in result.stderr


def test_both_spellings_are_the_same_command_object():
    """An alias, not a second implementation that can drift."""
    task_group = main.commands["task"]
    tmux_group = main.commands["tmux"]

    assert task_group.commands["id"] is tmux_group.commands["task"]


def test_no_task_does_not_reach_the_database(fake_go, monkeypatch):
    """Python owns no DB access here; the Go binary is the only reader."""
    def explode(*args, **kwargs):
        raise AssertionError("Python must not open the database for `task id`")

    from endless import db
    monkeypatch.setattr(db, "get_db", explode)

    result = CliRunner().invoke(main, ["task", "id"])

    assert result.exit_code == 0


def test_missing_binary_is_a_clean_error(monkeypatch):
    monkeypatch.setattr(tmux_cmd.shutil, "which", lambda _: None)

    result = CliRunner().invoke(main, ["task", "id"])

    assert result.exit_code != 0
    assert "endless-go binary not found" in result.output


def test_subprocess_module_is_the_real_one():
    """Guards the monkeypatch target above from a silent import rename."""
    assert tmux_cmd.subprocess is subprocess
