"""Tests for `endless session cd` (E-990)."""

import json
import os
from pathlib import Path

import pytest

from endless import session_cmd


def _write_companion(sessions_dir: Path, **fields) -> Path:
    """Write a companion file with sensible defaults; override via kwargs."""
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
def registered_with_sessions(registered_project, monkeypatch):
    """Registered project with .endless/sessions/ ready and cwd inside it."""
    sessions_dir = registered_project / ".endless" / "sessions"
    sessions_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.chdir(registered_project)
    return registered_project, sessions_dir


def test_explicit_numeric_match(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(sessions_dir, endless_session_id=42, cwd="/some/where")

    session_cmd.session_cd_resolve("42")
    out = capsys.readouterr()
    assert out.out.strip() == "/some/where"
    assert out.err == ""


def test_explicit_uuid_prefix_match(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(
        sessions_dir,
        harness_session_id="abcdef12-3456-7890-1234-567890abcdef",
        cwd="/uuid/match",
    )

    session_cmd.session_cd_resolve("abcdef12")
    out = capsys.readouterr()
    assert out.out.strip() == "/uuid/match"


def test_no_match(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(sessions_dir, endless_session_id=42)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve("999")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No Claude session matches" in err


def test_ambiguous_uuid_prefix(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(
        sessions_dir,
        harness_session_id="aaaa1111-0000-0000-0000-000000000001",
        endless_session_id=1,
    )
    _write_companion(
        sessions_dir,
        harness_session_id="aaaa2222-0000-0000-0000-000000000002",
        endless_session_id=2,
    )

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve("aaaa")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "Ambiguous" in err


def test_tmux_sibling_auto_resolve(registered_with_sessions, monkeypatch, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(sessions_dir, pane_id="%99", cwd="/sibling/cwd")

    monkeypatch.setenv("TMUX", "/tmp/tmux-501/default,1234,0")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(
        session_cmd, "_tmux_window_pane_ids", lambda: ["%53", "%99"]
    )

    session_cmd.session_cd_resolve(None)
    out = capsys.readouterr()
    assert out.out.strip() == "/sibling/cwd"


def test_tmux_no_sibling(registered_with_sessions, monkeypatch, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(sessions_dir, pane_id="%53")  # only my own pane

    monkeypatch.setenv("TMUX", "/tmp/x")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(
        session_cmd, "_tmux_window_pane_ids", lambda: ["%53"]
    )

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No sibling Claude pane" in err


def test_tmux_multiple_siblings(registered_with_sessions, monkeypatch, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(
        sessions_dir, pane_id="%99",
        harness_session_id="aaaa-1111-2222-3333-444444444444",
        endless_session_id=10,
    )
    _write_companion(
        sessions_dir, pane_id="%100",
        harness_session_id="bbbb-1111-2222-3333-444444444444",
        endless_session_id=11,
    )

    monkeypatch.setenv("TMUX", "/tmp/x")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(
        session_cmd, "_tmux_window_pane_ids", lambda: ["%53", "%99", "%100"]
    )

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "Multiple sibling Claude panes" in err


def test_no_tmux_no_arg_errors(registered_with_sessions, monkeypatch, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(sessions_dir)

    monkeypatch.delenv("TMUX", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "explicit session id is required" in err


def test_stale_companion_pruned(registered_with_sessions, monkeypatch, capsys):
    _, sessions_dir = registered_with_sessions
    stale = _write_companion(
        sessions_dir,
        pid=999999,  # very unlikely to be alive
        endless_session_id=42,
    )
    # Force-confirm staleness by patching liveness.
    monkeypatch.setattr(session_cmd, "_pid_alive", lambda pid: pid == os.getpid())

    with pytest.raises(SystemExit):
        session_cmd.session_cd_resolve("42")

    assert not stale.exists(), "stale companion should have been pruned"


def test_show_all_lists_candidates(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(
        sessions_dir,
        endless_session_id=10,
        harness_session_id="aaaaaaaa-1111-1111-1111-111111111111",
        cwd="/a",
        pane_id="%99",
    )
    _write_companion(
        sessions_dir,
        endless_session_id=11,
        harness_session_id="bbbbbbbb-2222-2222-2222-222222222222",
        cwd="/b",
        pane_id="%100",
    )

    session_cmd.session_cd_resolve(None, show_all=True)
    out = capsys.readouterr().out
    assert "/a" in out
    assert "/b" in out
    assert "10" in out
    assert "11" in out


def test_show_all_empty(registered_with_sessions, capsys):
    with pytest.raises(SystemExit) as exc:
        session_cmd.session_cd_resolve(None, show_all=True)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No live Claude sessions" in err


def test_worktree_path_preferred_when_set(registered_with_sessions, capsys):
    _, sessions_dir = registered_with_sessions
    _write_companion(
        sessions_dir,
        endless_session_id=42,
        cwd="/the/cwd",
        worktree_path="/the/worktree",
    )

    session_cmd.session_cd_resolve("42")
    assert capsys.readouterr().out.strip() == "/the/worktree"
