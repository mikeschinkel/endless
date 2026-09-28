"""`endless sandbox reset` and the worktree-creation call to it (E-1608).

The reset itself is Go (internal/sandboxcmd/reset_test.go). These pin the
Python side: the shim execs `endless-go sandbox reset`, and worktree creation
seeds through it non-fatally.
"""

import subprocess
from types import SimpleNamespace

import pytest
from click.testing import CliRunner

from endless import cli, sandbox_cmd


def test_cli_execs_endless_go_sandbox_reset(monkeypatch):
    calls = []
    monkeypatch.setattr(sandbox_cmd, "_resolve_endless_go", lambda: "endless-go")
    monkeypatch.setattr(
        sandbox_cmd.subprocess, "run",
        lambda cmd, **kw: calls.append(cmd) or SimpleNamespace(returncode=0),
    )
    result = CliRunner().invoke(cli.main, ["sandbox", "reset"])
    assert result.exit_code == 0, result.output
    assert calls == [["endless-go", "sandbox", "reset"]]


@pytest.mark.no_sandbox_reset_stub
def test_reset_after_create_runs_in_the_worktree(tmp_path, monkeypatch):
    calls = []
    monkeypatch.setattr(sandbox_cmd, "_resolve_endless_go", lambda: "endless-go")
    monkeypatch.setattr(
        sandbox_cmd.subprocess, "run",
        lambda cmd, **kw: calls.append((cmd, kw["cwd"])) or SimpleNamespace(returncode=0),
    )
    sandbox_cmd.reset_after_create(tmp_path)
    assert calls == [(["endless-go", "sandbox", "reset"], str(tmp_path))]


@pytest.mark.no_sandbox_reset_stub
def test_reset_after_create_failure_is_non_fatal_and_loud(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(sandbox_cmd, "_resolve_endless_go", lambda: "endless-go")
    monkeypatch.setattr(
        sandbox_cmd.subprocess, "run", lambda cmd, **kw: SimpleNamespace(returncode=4),
    )
    sandbox_cmd.reset_after_create(tmp_path)
    err = capsys.readouterr().err
    assert "exited 4" in err
    assert "endless sandbox reset" in err
    assert str(tmp_path) in err


def test_bootstrap_seeds_after_the_create_hook(tmp_path, monkeypatch):
    """Order matters: the create hook may build the binary the seed hook uses."""
    from endless import worktree_cmd

    order = []
    monkeypatch.setattr(worktree_cmd, "_run_post_worktree_create_hook",
                        lambda root, wt: order.append("create-hook"))
    monkeypatch.setattr(sandbox_cmd, "reset_after_create",
                        lambda wt: order.append("reset"))
    monkeypatch.setattr(worktree_cmd, "provision_worktree_sandbox", lambda wt: None)
    wt = tmp_path / "wt"
    wt.mkdir()
    worktree_cmd._bootstrap_task_worktree(1, wt, "main", "task/1", tmp_path)
    assert order == ["create-hook", "reset"]
