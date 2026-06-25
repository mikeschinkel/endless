"""Tests for the bg-agent attach verbs (E-1570).

Two verbs view a task's already-live `--bg` agent via `claude attach`:

- `endless task attach <id>` — execs the current process into `claude attach`
  (`task_attach_impl`). Refuses inside a Claude session unless --force.
- `endless task spawn --attach <id>` — opens a NEW tmux window running
  `claude attach` (`spawn_plan(attach=True)`). Mutually exclusive with --bg.

Both resolve the short id via `_lookup_bg_short_id`. `os.execvp`, `subprocess`,
and the tmux/`claude` shell-outs are mocked, so nothing real is launched.
"""

import os
import subprocess

import click
import pytest

from endless import db, task_cmd
from endless.task_cmd import _lookup_bg_short_id, spawn_plan, task_attach_impl


def _seed_bg_session(task_id: int, short_id: str, state: str = "working") -> None:
    """Insert a background (kind_id=2) sessions row bound to a task."""
    db.execute(
        "INSERT INTO sessions "
        "(session_id, platform, state, active_task_id, kind_id, short_id, "
        " started_at, last_activity) "
        "VALUES (NULL, 'claude', ?, ?, 2, ?, "
        " '2026-06-19T00:00:00', '2026-06-19T00:00:00')",
        (state, task_id, short_id),
    )


def _seed_project_and_task(task_id: int, title: str = "Bg task") -> None:
    """Insert a project + a single task so spawn_plan's lookup resolves."""
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('attach-test', '/tmp/attach-test', 'active', "
        "datetime('now'), datetime('now'))",
    )
    pid = db.query(
        "SELECT id FROM projects WHERE name = 'attach-test'"
    )[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (task_id, pid, title, "underway"),
    )


# --- _lookup_bg_short_id ---------------------------------------------------

def test_lookup_returns_short_id(isolated_env):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "abcd1234")
    assert _lookup_bg_short_id(1570) == "abcd1234"


def test_lookup_none_when_no_row(isolated_env):
    assert _lookup_bg_short_id(1570) is None


def test_lookup_ignores_non_working_state(isolated_env):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "abcd1234", state="ended")
    assert _lookup_bg_short_id(1570) is None


def test_lookup_returns_most_recent(isolated_env):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "old00000")
    _seed_bg_session(1570, "new11111")
    assert _lookup_bg_short_id(1570) == "new11111"


# --- task attach (exec) ----------------------------------------------------

def test_attach_no_bg_row_raises(isolated_env):
    with pytest.raises(click.ClickException):
        task_attach_impl(1570)


def test_attach_happy_path_execs(isolated_env, monkeypatch):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "abcd1234")
    calls = []
    monkeypatch.setattr(os, "execvp", lambda f, a: calls.append((f, list(a))))
    monkeypatch.delenv("CLAUDECODE", raising=False)

    task_attach_impl(1570)

    assert calls == [("claude", ["claude", "attach", "abcd1234"])]


def test_attach_inside_claude_session_refuses_without_force(isolated_env,
                                                            monkeypatch):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "abcd1234")
    called = []
    monkeypatch.setattr(os, "execvp", lambda f, a: called.append((f, a)))
    monkeypatch.setenv("CLAUDECODE", "1")

    with pytest.raises(SystemExit) as exc:
        task_attach_impl(1570)
    assert exc.value.code != 0
    assert called == []  # never exec'd — caller's session preserved


def test_attach_inside_claude_session_force_execs(isolated_env, monkeypatch):
    _seed_project_and_task(1570)
    _seed_bg_session(1570, "abcd1234")
    calls = []
    monkeypatch.setattr(os, "execvp", lambda f, a: calls.append((f, list(a))))
    monkeypatch.setenv("CLAUDECODE", "1")

    task_attach_impl(1570, force=True)

    assert calls == [("claude", ["claude", "attach", "abcd1234"])]


# --- spawn --attach (new tmux window) --------------------------------------

@pytest.fixture
def tmux_env(monkeypatch):
    """Satisfy spawn_plan's tmux gate and capture tmux shell-outs."""
    import shutil
    monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    # Avoid hitting ~/.local/bin/claude on the test machine.
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "claude")

    calls = []
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: calls.append(list(cmd)))
    return calls


def test_spawn_attach_no_bg_row_raises(isolated_env, tmux_env):
    _seed_project_and_task(1570)
    with pytest.raises(click.ClickException):
        spawn_plan(1570, attach=True)
    assert tmux_env == []  # never reached tmux


def test_spawn_attach_happy_path_opens_window(isolated_env, tmux_env):
    _seed_project_and_task(1570, title="Add attach verbs")
    _seed_bg_session(1570, "abcd1234")

    spawn_plan(1570, attach=True)

    # First tmux call creates the window; a later send-keys runs claude attach.
    assert tmux_env[0][:2] == ["tmux", "new-window"]
    send_keys = [c for c in tmux_env
                 if c[:2] == ["tmux", "send-keys"]
                 and any("attach abcd1234" in str(p) for p in c)]
    assert send_keys, f"no send-keys with claude attach found in {tmux_env}"
    assert "claude attach abcd1234" in send_keys[0]


def test_spawn_bg_and_attach_mutually_exclusive(isolated_env):
    with pytest.raises(click.ClickException):
        spawn_plan(1570, bg=True, attach=True)
