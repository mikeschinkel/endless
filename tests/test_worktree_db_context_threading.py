"""E-1429 (reopened): worktree_cmd's Go-subprocess spawns must thread the
resolved --db context so they aren't refused by the self-dev worktree gate when
run from inside a worktree.

Two sites were missed in the original E-1429 wiring:
  - _reap_stale_worktrees  -> `endless-go event reap-worktrees` (land's reap sweep)
  - _materialize_plan_file -> `endless-go session-query task-field` (claim)

Both open the DB and neither self-pins to main, so each needs the context
threaded when a --db context is resolved, and nothing when it isn't.

E-1668 changed the SPELLING, not the requirement: the child is told `--db main`
/ `--db sandbox` when the resolved dir is one of the two named databases, and
`--db-dir <path>` only when it is neither. These tests assert both branches,
because "a flag was threaded" was never the property — "the child opens the same
database this process did" is.
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


def test_reap_threads_db_main_when_resolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    worktree_cmd._reap_stale_worktrees(Path("/proj"))
    cmd = capture_spawn["cmd"]
    assert "--db" in cmd
    assert cmd[cmd.index("--db") + 1] == "main"
    assert cmd.index("--db") < cmd.index("reap-worktrees")


def test_reap_threads_db_dir_for_a_directory_that_is_neither(
    capture_spawn, monkeypatch
):
    """The escape, and only the escape: a resolved dir that is neither named
    database still has to reach the child, or the child opens a different one."""
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", Path("/home/x/.config/endless")
    )
    worktree_cmd._reap_stale_worktrees(Path("/proj"))
    cmd = capture_spawn["cmd"]
    assert "--db-dir" in cmd
    assert cmd[cmd.index("--db-dir") + 1] == "/home/x/.config/endless"
    assert cmd.index("--db-dir") < cmd.index("reap-worktrees")


def test_reap_omits_flag_when_unresolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._reap_stale_worktrees(Path("/proj"))
    cmd = capture_spawn["cmd"]
    assert "--db" not in cmd
    assert "--db-dir" not in cmd


def test_materialize_threads_db_main_when_resolved(
    capture_spawn, monkeypatch, tmp_path
):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    worktree_cmd._materialize_plan_file(1429, tmp_path)
    cmd = capture_spawn["cmd"]
    assert cmd[cmd.index("--db") + 1] == "main"
    assert cmd.index("--db") < cmd.index("task-field")


def test_materialize_omits_flag_when_unresolved(
    capture_spawn, monkeypatch, tmp_path
):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._materialize_plan_file(1429, tmp_path)
    assert "--db" not in capture_spawn["cmd"]
    assert "--db-dir" not in capture_spawn["cmd"]


# E-1947 added a third site: `endless-go worktree in-use`, drop's guard. It
# READS the sessions table, so an unthreaded --db would ask the real ledger
# about a sandbox's sessions and be told nobody is home — a guard that answers
# from the wrong database is worse than no guard, because it reads as a pass.


def test_in_use_guard_threads_db_main_when_resolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    worktree_cmd._guard_worktree_in_use(
        Path("/proj/.endless/worktrees/e-1947")
    )
    cmd = capture_spawn["cmd"]
    assert cmd[cmd.index("--db") + 1] == "main"
    assert cmd.index("--db") < cmd.index("worktree")


def test_in_use_guard_omits_flag_when_unresolved(capture_spawn, monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    worktree_cmd._guard_worktree_in_use(
        Path("/proj/.endless/worktrees/e-1947")
    )
    assert "--db" not in capture_spawn["cmd"]
    assert "--db-dir" not in capture_spawn["cmd"]


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
