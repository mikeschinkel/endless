"""E-1429 (reopened): worktree_cmd's Go-subprocess spawns must thread the
resolved --db context (--config-dir) so they aren't refused by the self-dev
worktree gate when run from inside a worktree.

Two sites were missed in the original E-1429 wiring:
  - _reap_stale_worktrees  -> `endless-go event reap-worktrees` (land's reap sweep)
  - _materialize_plan_file -> `endless-go session-query task-field` (claim)

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
    assert cmd.index("--config-dir") < cmd.index("task-field")


def test_materialize_omits_flag_when_unresolved(
    capture_spawn, monkeypatch, tmp_path
):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._materialize_plan_file(1429, tmp_path)
    assert "--config-dir" not in capture_spawn["cmd"]


# E-1947 added a third site: `endless-go worktree in-use`, drop's guard. It
# READS the sessions table, so an unthreaded --db would ask the real ledger
# about a sandbox's sessions and be told nobody is home — a guard that answers
# from the wrong database is worse than no guard, because it reads as a pass.


def test_in_use_guard_threads_config_dir_when_resolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", Path("/home/x/.config/endless")
    )
    worktree_cmd._guard_worktree_in_use(
        Path("/proj/.endless/worktrees/e-1947")
    )
    cmd = capture_spawn["cmd"]
    assert "--config-dir" in cmd
    assert cmd[cmd.index("--config-dir") + 1] == "/home/x/.config/endless"
    assert cmd.index("--config-dir") < cmd.index("worktree")


def test_in_use_guard_omits_flag_when_unresolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._guard_worktree_in_use(
        Path("/proj/.endless/worktrees/e-1947")
    )
    assert "--config-dir" not in capture_spawn["cmd"]


def test_in_use_guard_passes_the_path_derived_task_id(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._guard_worktree_in_use(
        Path("/proj/.endless/worktrees/e-1947")
    )
    cmd = capture_spawn["cmd"]
    assert cmd[cmd.index("--task") + 1] == "1947"


def test_in_use_guard_passes_task_zero_outside_the_convention(
    capture_spawn, monkeypatch
):
    """A worktree whose path encodes no task has no sessions row to look up,
    so the verb is told 0 and runs only the live-process probe."""
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._guard_worktree_in_use(Path("/somewhere/else/scratch"))
    cmd = capture_spawn["cmd"]
    assert cmd[cmd.index("--task") + 1] == "0"
