"""E-1429 (reopened): worktree_cmd's Go-subprocess spawns must thread the
resolved --db context (--config-dir) so they aren't refused by the self-dev
worktree gate when run from inside a worktree.

Two sites were missed in the original E-1429 wiring:
  - _reap_stale_worktrees  -> `endless-go event reap-worktrees` (land's reap sweep)
  - _materialize_plan_file -> `endless-go session-query task-text` (claim)

Both open the DB and neither self-pins to main, so each needs --config-dir
threaded when a --db context is resolved, and nothing when it isn't.
"""

import subprocess
from pathlib import Path

import pytest

from endless import config, worktree_cmd


class _FakeResult:
    returncode = 0
    stdout = ""
    stderr = ""


@pytest.fixture
def capture_spawn(monkeypatch):
    monkeypatch.setattr("shutil.which", lambda name: f"/usr/local/bin/{name}")
    captured = {}

    def fake_run(cmd, **kw):
        captured["cmd"] = cmd
        return _FakeResult()

    monkeypatch.setattr(subprocess, "run", fake_run)
    return captured


def test_reap_threads_config_dir_when_resolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", Path("/home/x/.config/endless")
    )
    worktree_cmd._reap_stale_worktrees(Path("/proj"))
    cmd = capture_spawn["cmd"]
    assert "--config-dir" in cmd
    assert cmd[cmd.index("--config-dir") + 1] == "/home/x/.config/endless"
    assert cmd.index("--config-dir") < cmd.index("reap-worktrees")


def test_reap_omits_flag_when_unresolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._reap_stale_worktrees(Path("/proj"))
    assert "--config-dir" not in capture_spawn["cmd"]


def test_materialize_threads_config_dir_when_resolved(
    capture_spawn, monkeypatch, tmp_path
):
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", Path("/home/x/.config/endless")
    )
    worktree_cmd._materialize_plan_file(1429, tmp_path)
    cmd = capture_spawn["cmd"]
    assert "--config-dir" in cmd
    assert cmd.index("--config-dir") < cmd.index("task-text")


def test_materialize_omits_flag_when_unresolved(
    capture_spawn, monkeypatch, tmp_path
):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._materialize_plan_file(1429, tmp_path)
    assert "--config-dir" not in capture_spawn["cmd"]
