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
    # E-1993: a task needs a plan before it can be spawned.
    db.execute("INSERT INTO task_content (task_id, name, content) VALUES (?, 'plan', '# Plan')", (task_id,))


@pytest.fixture
def fg_env(monkeypatch):
    """Satisfy the tmux gate and stub the pre-claim / render machinery so only
    the launch mechanics run. Returns the captured subprocess.run cmd list."""
    monkeypatch.setattr(shutil, "which", lambda name: f"/usr/bin/{name}")
    monkeypatch.setenv("TMUX", "/tmp/tmux-sock,1,0")
    monkeypatch.setattr(task_cmd, "_claude_binary", lambda: "claude")
    monkeypatch.setattr(task_cmd, "_check_task_ownership",
                        lambda *a, **k: None)
    # subprocess.run is captured below, so the open-questions read (a Go
    # call) cannot answer; this task has none.
    monkeypatch.setattr(task_cmd, "_open_questions", lambda item_id: [])
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


def test_foreground_spawn_window_named_for_the_task_alone(isolated_env, fg_env):
    """E-2102: the window name is the task id, with no project or title slug.

    A tab is narrow, and the words that used to fill it — the project name and
    the first two words of the title — are things the user already knows. The
    name is also read back by `internal/sandboxcmd/reapguard.go` to decide
    which DB sandboxes to spare, so its form is an interface.
    """
    _seed_project_and_task(1705, title="A task with a long and wordy title")

    spawn_plan(1705)

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert cmd[cmd.index("--window-name") + 1] == "E-1705"


def test_manual_spawn_passes_no_auto_flags(isolated_env, fg_env):
    """E-1814: a person's spawn is unchanged — not detached, not marked, and
    landing in the spawner's own session."""
    _seed_project_and_task(1705)

    spawn_plan(1705)

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert "--auto" not in cmd
    assert "--target-session" not in cmd


def test_auto_spawn_threads_auto_and_target_session(isolated_env, fg_env):
    """E-1814: the auto-spawn job's spawn reaches spawn-window as --auto (open
    detached, mark the window) plus the session it resolved."""
    _seed_project_and_task(1705)

    spawn_plan(1705, auto=True, target_session="$4")

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert "--auto" in cmd
    assert cmd[cmd.index("--target-session") + 1] == "$4"
    # No session asked for it, so none is named as its spawner — not even the
    # one this process could resolve.
    assert cmd[cmd.index("--spawned-by") + 1] == "auto-spawn"


def test_named_target_session_does_not_require_being_in_tmux(
        isolated_env, fg_env, monkeypatch):
    """E-1814: the job may run where $TMUX is unset (`endless jobs run` from a
    plain shell); a named target needs a reachable server, not a pane. Without
    one, the old refusal still holds."""
    _seed_project_and_task(1705)
    monkeypatch.delenv("TMUX")

    spawn_plan(1705, auto=True, target_session="$4")
    assert [c for c in fg_env if "spawn-window" in c]

    import click
    with pytest.raises(click.ClickException, match="Not in a tmux session"):
        spawn_plan(1705)


def test_auto_flags_are_hidden():
    from endless.cli import task_spawn
    hidden = {opt for p in task_spawn.params if getattr(p, "hidden", False)
              for opt in p.opts}
    assert {"--auto", "--target-session"} <= hidden


def test_foreground_spawn_session_named_for_the_task_by_default(isolated_env, fg_env):
    """E-2181: with no --name, the session is `e-NNNN` — the worktree basename
    without the two-character disambiguator Claude Code appends to an unnamed
    session. A session name is an address (ListAgents, SendMessage), so it
    must be derivable from the task id alone. An auto-spawn (E-1814) goes
    through the same default."""
    _seed_project_and_task(1705)

    for kwargs in ({}, {"auto": True, "target_session": "$4"}):
        fg_env.clear()
        spawn_plan(1705, **kwargs)

        cmd = [c for c in fg_env if "spawn-window" in c][0]
        assert cmd.count("--name") == 1, kwargs
        assert cmd[cmd.index("--name") + 1] == "e-1705", kwargs


def test_foreground_spawn_explicit_name_wins(isolated_env, fg_env):
    _seed_project_and_task(1705)

    spawn_plan(1705, name="foo")

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert cmd.count("--name") == 1
    assert cmd[cmd.index("--name") + 1] == "foo"


# E-2234: focus and placement flags.

def test_manual_spawn_defaults_to_first_and_refocus(isolated_env, fg_env):
    """A plain manual spawn puts its tab first and takes focus."""
    _seed_project_and_task(1705)

    spawn_plan(1705)

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert cmd[cmd.index("--placement") + 1] == "first"
    assert "--no-refocus" not in cmd
    assert "--tmux-session" not in cmd


def test_spawn_threads_placement_no_refocus_and_tmux_session(
        isolated_env, fg_env, monkeypatch):
    _seed_project_and_task(1705)
    monkeypatch.setattr(task_cmd, "_tmux_session_exists", lambda name: True)
    monkeypatch.delenv("TMUX")  # a named session needs a server, not a pane

    spawn_plan(1705, placement="right", no_refocus=True, tmux_session="work")

    cmd = [c for c in fg_env if "spawn-window" in c][0]
    assert cmd[cmd.index("--placement") + 1] == "right"
    assert "--no-refocus" in cmd
    assert cmd[cmd.index("--tmux-session") + 1] == "work"
    assert "--auto" not in cmd


def test_missing_tmux_session_refuses_before_pre_claim(
        isolated_env, fg_env, monkeypatch):
    """A name no session has is refused before anything happens — no
    pre-claim, no worktree, no window."""
    _seed_project_and_task(1705)
    monkeypatch.setattr(task_cmd, "_tmux_session_exists", lambda name: False)
    claimed = []
    monkeypatch.setattr(task_cmd, "_perform_claim_work",
                        lambda **k: claimed.append(k))

    import click
    with pytest.raises(click.ClickException, match="No tmux session is named 'nope'"):
        spawn_plan(1705, tmux_session="nope")
    assert claimed == []
    assert not [c for c in fg_env if "spawn-window" in c]


def _invoke_spawn(monkeypatch, *args):
    from click.testing import CliRunner
    from endless.cli import main
    calls = []
    monkeypatch.setattr(task_cmd, "spawn_plan",
                        lambda *a, **k: calls.append(k))
    result = CliRunner().invoke(main, ["task", "spawn", "E-1705", *args])
    return result, calls


@pytest.mark.parametrize("flag,placement", [
    ([], "first"), (["--to-first"], "first"), (["--to-last"], "last"),
    (["--to-left"], "left"), (["--to-right"], "right"),
])
def test_cli_maps_to_flags_to_placement(isolated_env, monkeypatch, flag, placement):
    result, calls = _invoke_spawn(monkeypatch, *flag)
    assert result.exit_code == 0, result.output
    assert calls[0]["placement"] == placement
    assert calls[0]["no_refocus"] is False


def test_cli_to_flags_are_mutually_exclusive(isolated_env, monkeypatch):
    result, calls = _invoke_spawn(monkeypatch, "--to-first", "--to-left")
    assert result.exit_code == 2
    assert "mutually exclusive" in result.output
    assert calls == []


def test_cli_threads_no_refocus_and_tmux_session(isolated_env, monkeypatch):
    result, calls = _invoke_spawn(monkeypatch, "--no-refocus",
                                  "--tmux-session", "work")
    assert result.exit_code == 0, result.output
    assert calls[0]["no_refocus"] is True
    assert calls[0]["tmux_session"] == "work"
