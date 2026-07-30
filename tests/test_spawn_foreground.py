"""Tests for the foreground `endless task spawn` launch path (E-1705).

The foreground path launches Claude as the tmux window's command via a single
`endless-go spawn-window` invocation — no `tmux send-keys`, `load-buffer`,
`paste-buffer`, or `/plan` slash-command. The heavy pre-claim / worktree / git
machinery is stubbed so these tests isolate the launch mechanics.
"""

import shutil
import subprocess
from pathlib import Path

import pytest

from endless import db, task_cmd
from endless.task_cmd import spawn_plan


def _seed_project_and_task(task_id: int, title: str = "Deliver spawn") -> None:
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('fg-test', '/tmp/fg-test', 'active', "
        "datetime('now'), datetime('now'))",
    )
    pid = db.query("SELECT id FROM projects WHERE name = 'fg-test'")[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (task_id, pid, title, "underway"),
    )


@pytest.fixture
def fg_env(monkeypatch):
    """Satisfy the tmux gate and stub the pre-claim / render machinery so only
    the launch mechanics run. Returns the captured subprocess.run cmd list."""
    monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "claude")
    monkeypatch.setattr(task_cmd, "_check_task_ownership",
                        lambda *a, **k: None)
    monkeypatch.setattr(task_cmd, "_resolve_project", lambda *a, **k: (None, "p"))
    monkeypatch.setattr(task_cmd, "_perform_claim_work",
                        lambda **k: (Path("/wt/e-1705"), True))
    monkeypatch.setattr(task_cmd, "render_handoff", lambda *a, **k: "HANDOFF")
    monkeypatch.setattr(task_cmd, "_branch_for_worktree", lambda p: "br")
    monkeypatch.setattr(task_cmd, "_current_endless_session_id",
                        lambda *a, **k: "sess-1")

    calls = []
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: calls.append(list(cmd)))
    return calls


def test_foreground_spawn_single_launcher_call(isolated_env, fg_env):
    _seed_project_and_task(1705)

    spawn_plan(1705)

    launch = [c for c in fg_env if "spawn-window" in c]
    assert len(launch) == 1, f"want one spawn-window call, got {fg_env}"
    cmd = launch[0]
    # Positional-prompt delivery: handoff travels by file, permission mode auto.
    assert "--handoff-file" in cmd
    assert cmd[cmd.index("--permission-mode") + 1] == "auto"
    assert "--task-id" in cmd and "1705" in cmd
    assert cmd[cmd.index("--cwd") + 1] == "/wt/e-1705"


def test_foreground_spawn_no_send_keys_or_plan(isolated_env, fg_env):
    _seed_project_and_task(1705)

    spawn_plan(1705)

    # Zero keystroke-injection calls, and the /plan slash-command is never sent.
    for c in fg_env:
        assert c[:2] != ["tmux", "send-keys"], f"unexpected send-keys: {c}"
        assert not any(str(p) in ("load-buffer", "paste-buffer") for p in c), c
        assert "/plan" not in c, f"/plan must never be injected: {c}"


def test_foreground_spawn_threads_model_and_permission_mode(isolated_env, fg_env):
    _seed_project_and_task(1705)

    spawn_plan(1705, permission_mode="plan", model="claude-opus-4-8",
               name="E-1705")

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert cmd[cmd.index("--permission-mode") + 1] == "plan"
    assert cmd[cmd.index("--model") + 1] == "claude-opus-4-8"
    assert cmd[cmd.index("--name") + 1] == "E-1705"


def test_spawn_has_no_no_plan_flag():
    # The CLI must no longer expose --no-plan (E-1705 dropped it).
    from endless.cli import task_spawn
    flags = {opt for p in task_spawn.params for opt in getattr(p, "opts", [])}
    assert "--no-plan" not in flags
    assert "--permission-mode" in flags
