"""Tests for `endless session use` (E-1014) — shell-evaluable activation."""

import json
import os
import shlex
from pathlib import Path

import pytest

from endless import session_cmd


def _write_companion(sessions_dir: Path, **fields) -> Path:
    sessions_dir.mkdir(parents=True, exist_ok=True)
    data = {
        "harness": "claude",
        "harness_session_id": "f41f263e-c708-4c42-af7c-083b5be04943",
        "endless_session_id": 247,
        "pane_id": "%53",
        "cwd": "/Users/mike/Projects/endless",
        "pid": os.getpid(),
        "started_at": "2026-04-29T03:51:23Z",
    }
    data.update(fields)
    path = sessions_dir / f"{data['harness']}-{data['harness_session_id']}.json"
    path.write_text(json.dumps(data))
    return path


@pytest.fixture
def project_with_companion(registered_project, monkeypatch):
    sessions_dir = registered_project / ".endless" / "sessions"
    sessions_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.chdir(registered_project)
    return registered_project, sessions_dir


# ---------- default activation block ----------------------------------------

def test_use_emits_default_block(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, cwd=str(project_root))

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out

    assert f"cd {shlex.quote(str(project_root))}" in out
    assert "export ENDLESS_SESSION_ID=247" in out
    assert "export ENDLESS_HARNESS_SESSION_ID=f41f263e-c708-4c42-af7c-083b5be04943" in out
    assert "export ENDLESS_HARNESS=claude" in out
    assert f"export ENDLESS_PROJECT_ROOT={shlex.quote(str(project_root))}" in out
    assert "export ENDLESS_WORKTREE_PATH=''" in out  # shlex.quote('') == "''"


def test_use_prefers_worktree_path(project_with_companion, capsys):
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir, worktree_path="/some/worktree", cwd="/the/cwd")

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert "cd /some/worktree" in out
    assert "export ENDLESS_WORKTREE_PATH=/some/worktree" in out


def test_use_no_extension_only_default_block(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    # No extension exists; output is exactly the default block (6 lines).
    assert out.count("\n") == 6
    assert "endless/extensions" not in out  # no warnings about extension


# ---------- extension execution ---------------------------------------------

def _write_extension(project_root: Path, body: str, executable: bool = True) -> Path:
    ext_dir = project_root / ".endless" / "extensions"
    ext_dir.mkdir(parents=True, exist_ok=True)
    path = ext_dir / "use.sh"
    path.write_text(body)
    if executable:
        os.chmod(path, 0o644)  # owner rw, no world-write
    return path


def test_use_extension_stdout_appended(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    _write_extension(
        project_root,
        '#!/bin/sh\necho "export FOO=bar"\necho "alias hi=\'echo hi\'"\n',
    )

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert "export FOO=bar" in out
    assert "alias hi='echo hi'" in out
    # Default block still present
    assert "export ENDLESS_SESSION_ID=247" in out


def test_use_extension_receives_env_vars(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, worktree_path="/the/wt")
    _write_extension(
        project_root,
        '#!/bin/sh\necho "export GOT_SESSION=$ENDLESS_SESSION_ID"\n'
        'echo "export GOT_WT=$ENDLESS_WORKTREE_PATH"\n'
        'echo "export GOT_HARNESS=$ENDLESS_HARNESS"\n',
    )

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert "export GOT_SESSION=247" in out
    assert "export GOT_WT=/the/wt" in out
    assert "export GOT_HARNESS=claude" in out


def test_use_extension_nonzero_exit_warns_but_continues(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    _write_extension(
        project_root,
        '#!/bin/sh\necho "export PARTIAL=yes"\nexit 7\n',
    )

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    # Warning to stderr
    assert "exited 7" in cap.err
    # Partial output still appended to stdout
    assert "export PARTIAL=yes" in cap.out
    # Default block still emitted
    assert "export ENDLESS_SESSION_ID=247" in cap.out


def test_use_extension_stderr_passed_through(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    _write_extension(
        project_root,
        '#!/bin/sh\necho "diagnostic message" >&2\necho "export OK=1"\n',
    )

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "diagnostic message" in cap.err
    assert "[use.sh]" in cap.err
    assert "export OK=1" in cap.out


def test_use_extension_world_writable_refused(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    ext = _write_extension(project_root, '#!/bin/sh\necho "export EVIL=1"\n')
    os.chmod(ext, 0o646)  # world-writable

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "world-writable" in cap.err
    # Extension output NOT in stdout
    assert "export EVIL=1" not in cap.out
    # Default block still emitted
    assert "export ENDLESS_SESSION_ID=247" in cap.out


def test_use_extension_timeout(project_with_companion, monkeypatch, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    _write_extension(
        project_root,
        '#!/bin/sh\nsleep 1\necho "export NEVER=1"\n',
    )
    monkeypatch.setattr(session_cmd, "_USE_EXTENSION_TIMEOUT_SEC", 0.1)

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "timed out" in cap.err
    assert "export NEVER=1" not in cap.out
    # Default block still emitted
    assert "export ENDLESS_SESSION_ID=247" in cap.out


def test_use_no_extension_dir_silent(project_with_companion, capsys):
    """When .endless/extensions/ doesn't exist at all, no warnings."""
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir)

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert cap.err == ""


# ---------- resolution (mirrors session cd / show) --------------------------

def test_use_no_arg_in_tmux(project_with_companion, monkeypatch, capsys):
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir, pane_id="%99", cwd="/sibling/cwd")
    monkeypatch.setenv("TMUX", "/tmp/x")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%53", "%99"])

    session_cmd.session_use_resolve(None)
    out = capsys.readouterr().out
    assert "cd /sibling/cwd" in out


def test_use_no_arg_outside_tmux_errors(project_with_companion, monkeypatch, capsys):
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir)
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_use_resolve(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "explicit session id is required" in err


def test_use_unknown_id_errors(project_with_companion, capsys):
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_use_resolve("99999")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No Claude session matches" in err
