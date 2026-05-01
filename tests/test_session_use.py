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

def test_use_emits_minimal_block(project_with_companion, capsys):
    """Activation block is minimal: cd + ENDLESS_SESSION_ID. (E-1038.)"""
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, cwd=str(project_root))

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out

    assert f"cd {shlex.quote(str(project_root))}" in out
    assert "export ENDLESS_SESSION_ID=247" in out
    # Other ENDLESS_* vars are no longer in the activation block — fields
    # are looked up on demand via 'endless session show <id> --json'.
    assert "ENDLESS_HARNESS_SESSION_ID" not in out
    assert "ENDLESS_HARNESS=" not in out
    assert "ENDLESS_PROJECT_ROOT" not in out
    assert "ENDLESS_WORKTREE_PATH" not in out


def test_use_cds_to_existing_worktree(project_with_companion, capsys, tmp_path):
    _, sessions_dir = project_with_companion
    real_worktree = tmp_path / "real-worktree"
    real_worktree.mkdir()
    _write_companion(sessions_dir, worktree_path=str(real_worktree), cwd="/the/cwd")

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert f"cd {shlex.quote(str(real_worktree))}" in out


def test_use_falls_back_to_cwd_when_worktree_missing(project_with_companion, capsys):
    """If companion's worktree_path doesn't exist on disk, cd to cwd. (E-1037 / E-1038.)"""
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir, worktree_path="/this/path/does/not/exist", cwd="/the/cwd")

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert "cd /the/cwd" in out
    assert "/this/path/does/not/exist" not in out


def test_use_no_extension_only_minimal_block(project_with_companion, capsys):
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir)

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    # Two lines: cd + one export.
    assert out.count("\n") == 2
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


def test_use_extension_receives_only_session_id(project_with_companion, capsys):
    """Extension's env contains ENDLESS_SESSION_ID and nothing else
    ENDLESS_*-prefixed. Other fields are looked up via 'endless session show'.
    (E-1038.)
    """
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, worktree_path="/the/wt")
    _write_extension(
        project_root,
        '#!/bin/sh\n'
        'echo "export GOT_SESSION=$ENDLESS_SESSION_ID"\n'
        'echo "export GOT_WT=${ENDLESS_WORKTREE_PATH:-unset}"\n'
        'echo "export GOT_HARNESS=${ENDLESS_HARNESS:-unset}"\n'
        'echo "export GOT_PROJECT=${ENDLESS_PROJECT_ROOT:-unset}"\n',
    )

    session_cmd.session_use_resolve("247")
    out = capsys.readouterr().out
    assert "export GOT_SESSION=247" in out
    # The dropped vars must not leak into the extension's env.
    assert "export GOT_WT=unset" in out
    assert "export GOT_HARNESS=unset" in out
    assert "export GOT_PROJECT=unset" in out


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


def test_use_no_extension_no_warning(project_with_companion, capsys):
    """When no extension exists, only the status line appears on stderr —
    no extension-related warnings (E-1047)."""
    _, sessions_dir = project_with_companion
    _write_companion(sessions_dir)

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    # Status line is expected on stderr (E-1047); no warnings.
    assert "Session 247" in cap.err
    assert "warning" not in cap.err
    assert "extension" not in cap.err
    assert "!" not in cap.err  # no stale-worktree warning


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


# ---------- status messages (E-1047) ----------------------------------------

def test_use_emits_status_to_stderr(project_with_companion, capsys):
    """Successful activation emits a • Session N → <path> line to stderr."""
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, cwd=str(project_root))

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "•" in cap.err
    assert "Session 247" in cap.err
    assert str(project_root) in cap.err
    # stdout has only the eval block — no status leaks there.
    assert "•" not in cap.out
    assert "Session 247" not in cap.out


def test_use_warns_when_worktree_path_stale(project_with_companion, capsys):
    """Companion's worktree_path set but dir gone -> stderr warning + status."""
    _, sessions_dir = project_with_companion
    _write_companion(
        sessions_dir,
        worktree_path="/this/is/very/much/gone",
        cwd="/the/cwd",
    )

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "/this/is/very/much/gone" in cap.err
    assert "no longer exists" in cap.err
    assert "falling back to cwd" in cap.err
    # Status line still appears.
    assert "Session 247" in cap.err
    # cd target on stdout is the cwd fallback.
    assert "cd /the/cwd" in cap.out


def test_use_silent_when_no_worktree_path_set(project_with_companion, capsys):
    """No worktree_path on companion (no active task) -> no stale warning,
    just the status line."""
    project_root, sessions_dir = project_with_companion
    _write_companion(sessions_dir, cwd=str(project_root))  # no worktree_path

    session_cmd.session_use_resolve("247")
    cap = capsys.readouterr()
    assert "no longer exists" not in cap.err
    assert "falling back" not in cap.err
    assert "Session 247" in cap.err
