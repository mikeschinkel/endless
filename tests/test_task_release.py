"""Tests for `endless task release` (E-1243).

Exercises release_item across the four scenarios from E-1243:
  - Bare release with current session bound to a task
  - Bare release when current session has no claim
  - release E-NNN when no session has it (with and without --ignore-missing)
  - release E-NNN when a stale (no-live-companion) session has it
  - release E-NNN when a different LIVE session has it (refuse)
"""

from unittest.mock import patch

import click
import pytest

from endless import db


def _insert_session(
    *,
    pk: int,
    session_id: str,
    project_id: int,
    state: str = "working",
    active_task_id: int | None = None,
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, active_task_id) "
        "VALUES (?, ?, ?, 'claude', ?, '2026-05-11T00:00:00', ?)",
        (pk, session_id, project_id, state, active_task_id),
    )


def _insert_task(*, pk: int, project_id: int, status: str = "in_progress"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, ?, 'test task', ?)",
        (pk, project_id, status),
    )


@pytest.fixture
def project_at_cwd(seeded_project_at_cwd):
    return {
        "project_root": seeded_project_at_cwd,
        "project_id": db.query(
            "SELECT id FROM projects WHERE path = ?",
            (str(seeded_project_at_cwd),),
        )[0]["id"],
    }


def test_release_bare_no_current_session_errors_with_pointer():
    from endless.task_cmd import release_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        with pytest.raises(click.ClickException) as exc:
            release_item(None)
    assert "endless task release E-NNN" in str(exc.value)


def test_release_bare_current_session_has_no_claim(project_at_cwd, capsys):
    from endless.task_cmd import release_item
    _insert_session(pk=100, session_id="s-100", project_id=project_at_cwd["project_id"])

    with patch("endless.task_cmd._current_endless_session_id", return_value=100):
        release_item(None)

    captured = capsys.readouterr()
    assert "No task currently claimed" in captured.out


def test_release_id_no_session_has_it_errors_by_default(project_at_cwd):
    from endless.task_cmd import release_item
    _insert_task(pk=500, project_id=project_at_cwd["project_id"])

    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        with pytest.raises(click.ClickException) as exc:
            release_item(500)
    assert "E-500 is not currently claimed" in str(exc.value)


def test_release_id_no_session_has_it_with_ignore_missing(project_at_cwd, capsys):
    from endless.task_cmd import release_item
    _insert_task(pk=501, project_id=project_at_cwd["project_id"])

    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        release_item(501, ignore_missing=True)

    captured = capsys.readouterr()
    assert "E-501 is not currently claimed" in captured.out


def test_release_id_held_by_live_other_session_refuses(project_at_cwd, stage_live_session):
    """Refuse when a DIFFERENT live session has the task bound."""
    from endless.task_cmd import release_item
    _insert_task(pk=600, project_id=project_at_cwd["project_id"])
    _insert_session(
        pk=200, session_id="s-200", project_id=project_at_cwd["project_id"],
        active_task_id=600,
    )
    stage_live_session(
        endless_session_id=200,
        harness_session_id="s-200-uuid",
        pane_id="%200",
    )

    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        with pytest.raises(click.ClickException) as exc:
            release_item(600)
    msg = str(exc.value)
    assert "E-600 is held by session 200" in msg
    assert "live" in msg
    # DB binding unchanged
    row = db.query("SELECT active_task_id FROM sessions WHERE id = 200")[0]
    assert row["active_task_id"] == 600


def test_release_id_stale_binding_auto_clears(project_at_cwd, capsys, stage_live_session):
    """No live companion → binding is stale → clear it with a notice."""
    from endless.task_cmd import release_item
    _insert_task(pk=700, project_id=project_at_cwd["project_id"])
    _insert_session(
        pk=300, session_id="s-300", project_id=project_at_cwd["project_id"],
        active_task_id=700,
    )
    # NOTE: stage_live_session fixture is taken to activate the _live_sessions
    # patch; nothing is staged so the patched _live_sessions returns [],
    # mirroring the no-live-companion / stale-binding scenario.

    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        release_item(700)

    captured = capsys.readouterr()
    assert "clearing stale binding for E-700" in captured.out
    assert "session 300 is no longer alive" in captured.out
    assert "released claim on E-700" in captured.out
