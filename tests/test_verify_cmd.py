"""Tests for the `endless task verify` Python wrapper (E-1603 / E-1605).

`run_verify` is a thin shell to `endless-go verify`: it resolves the task id and
the binary, composes the argv, and propagates the child's exit code. These tests
pin that contract with the subprocess and resolvers stubbed — no real endless-go
runs — so they are the fast unit proof that the wrapper builds the command
correctly and never swallows the runner's exit status.

This module is also the reference `pytest/uv` check in E-1605's E-1603
verification suite (.endless/tasks/e-1603/verify.toml): the exemplar that
exercises the pytest first-class runner end to end.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import verify_cmd


class _FakeProc:
    """Stand-in for subprocess.CompletedProcess carrying just the return code."""

    def __init__(self, returncode: int):
        self.returncode = returncode


def _stub_run(monkeypatch, returncode: int = 0) -> list:
    """Stub subprocess.run to record each argv and return a fake process, and
    give the wrapper a deterministic binary. Returns the list of recorded argvs.
    """
    calls: list = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return _FakeProc(returncode)

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(verify_cmd, "_resolve_endless_go", lambda: "endless-go")
    return calls


def test_explicit_id_builds_command_and_propagates_exit(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    with pytest.raises(SystemExit) as exc:
        verify_cmd.run_verify(1603, keep=False)
    assert exc.value.code == 0
    assert calls == [["endless-go", "verify", "E-1603"]]


def test_keep_flag_passed_through(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(1603, keep=True)
    assert calls == [["endless-go", "verify", "--keep", "E-1603"]]


def test_nonzero_exit_propagates(monkeypatch):
    _stub_run(monkeypatch, returncode=2)
    with pytest.raises(SystemExit) as exc:
        verify_cmd.run_verify(1603, keep=False)
    assert exc.value.code == 2


def test_none_id_resolves_active_task(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: 1758)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1758"]]


def test_none_id_falls_back_to_the_cwd_worktree(monkeypatch):
    """The session is the primary source; the checkout is the fallback.

    It needs no database, which is what makes a bare `endless task verify`
    work in a self-dev worktree with no --db and outside tmux (E-2023).
    """
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: None)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1889"]]


def test_session_wins_over_cwd(monkeypatch):
    """Standing in a foreign worktree must not retarget your own verify."""
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: 1758)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1758"]]


def test_an_unreadable_session_is_not_the_answer(monkeypatch):
    """A refused database read falls through to the cwd rather than failing.

    Inside a self-dev worktree, reading the session's task without --db raises.
    That is a reason to try the other source, not to report no task.
    """
    calls = _stub_run(monkeypatch, returncode=0)

    def boom():
        raise click.ClickException("--db is required here")

    monkeypatch.setattr(verify_cmd, "_current_session_task_id", boom)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1889"]]


def test_none_id_no_active_task_raises(monkeypatch):
    _stub_run(monkeypatch)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: None)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: None)
    with pytest.raises(click.ClickException):
        verify_cmd.run_verify(None, keep=False)


def test_cwd_task_id_reads_the_worktree_path():
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/e-1889")) == 1889
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/e-1889/src/x")) == 1889
    assert verify_cmd._cwd_task_id(Path("/p/src")) is None
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/scratch")) is None


def test_runs_in_the_tasks_worktree(monkeypatch, tmp_path):
    """A suite proves the CANDIDATE tree, so it runs there — not in main.

    Without this, asking for a task from the main checkout would discover
    main's copy of the suite and run it against code that has not landed.
    """
    worktree = tmp_path / ".endless" / "worktrees" / "e-1889"
    worktree.mkdir(parents=True)
    monkeypatch.setattr(verify_cmd, "_main_checkout", lambda: tmp_path)

    cwds: list = []

    def fake_run(cmd, **kwargs):
        cwds.append(kwargs.get("cwd"))
        return _FakeProc(0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(verify_cmd, "_resolve_endless_go", lambda: "endless-go")
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(1889, keep=False)
    assert cwds == [str(worktree)]


def test_missing_worktree_inherits_cwd(monkeypatch, tmp_path):
    """A reaped worktree, or a project not using them, still verifies."""
    monkeypatch.setattr(verify_cmd, "_main_checkout", lambda: tmp_path)
    assert verify_cmd._run_dir(1889) is None
