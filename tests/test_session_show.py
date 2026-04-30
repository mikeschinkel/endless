"""Tests for `endless session show` (E-991) and `session history` no-arg
default-to-current (E-992)."""

import json
import os
from pathlib import Path

import pytest

from endless import db, session_cmd


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


def _insert_session(
    *,
    pk: int,
    session_id: str,
    project_id: int,
    state: str = "idle",
    summary: str = "test summary",
    started_at: str = "2026-04-29T03:51:23",
    last_activity: str = "2026-04-29T05:00:00",
    active_task_id: int | None = None,
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, summary, "
        "started_at, last_activity, active_task_id) "
        "VALUES (?, ?, ?, 'claude', ?, ?, ?, ?, ?)",
        (pk, session_id, project_id, state, summary, started_at, last_activity, active_task_id),
    )


def _insert_task(*, pk: int, project_id: int, title: str = "Some work", status: str = "in_progress"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (pk, project_id, title, status),
    )


def _project_id(name: str) -> int:
    rows = db.query("SELECT id FROM projects WHERE name = ?", (name,))
    return rows[0]["id"]


@pytest.fixture
def project_with_session(registered_project, monkeypatch):
    sessions_dir = registered_project / ".endless" / "sessions"
    sessions_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.chdir(registered_project)
    pid = _project_id("my-project")
    _insert_session(pk=247, session_id="f41f263e-c708-4c42-af7c-083b5be04943", project_id=pid)
    return registered_project, sessions_dir, pid


# ---------- session show -----------------------------------------------------

def test_show_explicit_id(project_with_session, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir)

    session_cmd.session_show_resolve("247")
    out = capsys.readouterr().out
    assert "Session E-247" in out
    assert "f41f263e-c708-4c42-af7c-083b5be04943" in out
    assert "my-project" in out
    assert "idle" in out
    assert "Active task:   (none)" in out


def test_show_with_active_task(project_with_session, capsys):
    _, sessions_dir, pid = project_with_session
    _insert_task(pk=999, project_id=pid, title="Wire up backfill", status="in_progress")
    db.execute("UPDATE sessions SET active_task_id = 999 WHERE id = 247")
    _write_companion(sessions_dir)

    session_cmd.session_show_resolve("247")
    out = capsys.readouterr().out
    assert "Active task:   E-999" in out
    assert "[in_progress]" in out
    assert "Wire up backfill" in out


def test_show_json_output(project_with_session, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir, worktree_path="/some/worktree")

    session_cmd.session_show_resolve("247", as_json=True)
    out = capsys.readouterr().out
    data = json.loads(out)
    assert data["id"] == 247
    assert data["session_id"] == "f41f263e-c708-4c42-af7c-083b5be04943"
    assert data["project"] == "my-project"
    assert data["state"] == "idle"
    assert data["worktree_path"] == "/some/worktree"
    assert data["active_task"] is None


def test_show_summary_flattened(project_with_session, capsys):
    _, sessions_dir, _ = project_with_session
    db.execute(
        "UPDATE sessions SET summary = ? WHERE id = 247",
        ("Line one.\n\nLine two with    multiple   spaces.\nLine three.",),
    )
    _write_companion(sessions_dir)

    session_cmd.session_show_resolve("247")
    out = capsys.readouterr().out
    assert "Line one. Line two with multiple spaces. Line three." in out


def test_show_no_match(project_with_session, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_show_resolve("999")
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "No Claude session matches" in err


def test_show_uuid_prefix(project_with_session, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir)

    session_cmd.session_show_resolve("f41f263e")
    out = capsys.readouterr().out
    assert "Session E-247" in out


def test_show_no_arg_in_tmux(project_with_session, monkeypatch, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir, pane_id="%99")
    monkeypatch.setenv("TMUX", "/tmp/x")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%53", "%99"])

    session_cmd.session_show_resolve(None)
    out = capsys.readouterr().out
    assert "Session E-247" in out


def test_show_no_arg_outside_tmux(project_with_session, monkeypatch, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir)
    monkeypatch.delenv("TMUX", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.session_show_resolve(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "explicit session id is required" in err


# ---------- session history default-to-current ------------------------------

def test_history_no_arg_resolves_via_companion(project_with_session, monkeypatch, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir, pane_id="%99")
    # Insert one message so history has something to render.
    db.execute(
        "INSERT INTO session_messages (session_id, role, content, created_at) "
        "VALUES (?, 'user', 'hello', '2026-04-29T05:00:00')",
        ("f41f263e-c708-4c42-af7c-083b5be04943",),
    )
    monkeypatch.setenv("TMUX", "/tmp/x")
    monkeypatch.setenv("TMUX_PANE", "%53")
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%53", "%99"])

    session_cmd.show_history(None)
    out = capsys.readouterr().out
    assert "hello" in out


def test_history_no_arg_outside_tmux_errors(project_with_session, monkeypatch, capsys):
    _, sessions_dir, _ = project_with_session
    _write_companion(sessions_dir)
    monkeypatch.delenv("TMUX", raising=False)

    with pytest.raises(SystemExit) as exc:
        session_cmd.show_history(None)
    assert exc.value.code == 1
    err = capsys.readouterr().err
    assert "explicit session id is required" in err
