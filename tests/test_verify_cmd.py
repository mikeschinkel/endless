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


def test_none_id_no_active_task_raises(monkeypatch):
    _stub_run(monkeypatch)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: None)
    with pytest.raises(click.ClickException):
        verify_cmd.run_verify(None, keep=False)
