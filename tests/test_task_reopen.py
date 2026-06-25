"""Tests for `endless task reopen` and `task spawn --reopen` (E-1555).

Exercises reopen semantics from the E-1555 plan:
  - Reopen flips assumed/confirmed/completed → ready (text present) or
    unplanned (text absent).
  - Reopen refuses on declined/obsolete (steers to `task update --status`).
  - Reopen refuses on non-terminal statuses.
  - Reopen releases lingering session bindings to the task.
  - `task spawn --reopen` enforces explicit intent: errors on non-terminal
    targets, errors when both --reopen and --force are passed.
  - `task spawn` (no flag) on a reopenable terminal target points the user
    at --reopen in the error message.
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
        "VALUES (?, ?, ?, 'claude', ?, '2026-06-11T00:00:00', ?)",
        (pk, session_id, project_id, state, active_task_id),
    )


def _insert_task(
    *, pk: int, project_id: int, status: str = "assumed",
    text: str | None = None,
):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, text) "
        "VALUES (?, ?, 'test task', ?, ?)",
        (pk, project_id, status, text),
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


# ---------- reopen_item ----------


def test_reopen_assumed_with_text_promotes_to_ready(project_at_cwd, capsys):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1000, project_id=project_at_cwd["project_id"],
        status="assumed", text="# plan body\n",
    )

    reopen_item(1000)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (1000,))[0]
    assert row["status"] == "ready"

    captured = capsys.readouterr()
    assert "Updated E-1000" in captured.out
    assert "assumed -> ready" in captured.out
    assert "text: present" in captured.out


def test_reopen_confirmed_without_text_falls_to_unplanned(project_at_cwd, capsys):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1001, project_id=project_at_cwd["project_id"],
        status="confirmed", text=None,
    )

    reopen_item(1001)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (1001,))[0]
    assert row["status"] == "unplanned"

    captured = capsys.readouterr()
    assert "confirmed -> unplanned" in captured.out
    assert "text: absent" in captured.out


def test_reopen_completed_epic_promotes_to_ready(project_at_cwd, capsys):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1002, project_id=project_at_cwd["project_id"],
        status="completed", text="plan",
    )
    # Insert a child to confirm cascade=False — child is unaffected.
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, parent_id) "
        "VALUES (1003, ?, 'child', 'confirmed', 1002)",
        (project_at_cwd["project_id"],),
    )

    reopen_item(1002)

    parent = db.query("SELECT status FROM tasks WHERE id = 1002")[0]
    child = db.query("SELECT status FROM tasks WHERE id = 1003")[0]
    assert parent["status"] == "ready"
    assert child["status"] == "confirmed"


@pytest.mark.parametrize("status", ["ready", "unplanned", "underway",
                                    "unverified", "blocked", "revisit"])
def test_reopen_refuses_non_terminal_status(project_at_cwd, status):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1100, project_id=project_at_cwd["project_id"],
        status=status, text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        reopen_item(1100)
    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "terminal" in msg

    row = db.query("SELECT status FROM tasks WHERE id = 1100")[0]
    assert row["status"] == status


@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_reopen_refuses_declined_obsolete_with_pointer(project_at_cwd, status):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1110, project_id=project_at_cwd["project_id"],
        status=status, text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        reopen_item(1110)
    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "task update" in msg

    row = db.query("SELECT status FROM tasks WHERE id = 1110")[0]
    assert row["status"] == status


def test_reopen_unknown_id_errors(project_at_cwd):
    from endless.task_cmd import reopen_item

    with pytest.raises(click.ClickException) as exc:
        reopen_item(999999)
    assert "No task found" in str(exc.value)


def test_reopen_clears_lingering_session_binding(project_at_cwd):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1200, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )
    _insert_session(
        pk=400, session_id="s-400",
        project_id=project_at_cwd["project_id"],
        active_task_id=1200,
    )

    reopen_item(1200)

    row = db.query(
        "SELECT active_task_id FROM sessions WHERE id = 400",
    )[0]
    assert row["active_task_id"] is None


def test_reopen_does_not_create_worktree(project_at_cwd):
    """Reopen is metadata-only. No worktree created/touched."""
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1300, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )

    with patch("endless.worktree_cmd.create_task_worktree") as wt_mock:
        reopen_item(1300)

    assert not wt_mock.called


# ---------- spawn --reopen ----------


def test_spawn_reopen_and_force_mutually_exclusive(project_at_cwd):
    from endless.task_cmd import spawn_plan

    _insert_task(
        pk=1400, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        spawn_plan(1400, reopen=True, force=True)
    assert "mutually exclusive" in str(exc.value)


@pytest.mark.parametrize("status", ["ready", "unplanned", "underway",
                                    "unverified", "blocked", "revisit"])
def test_spawn_reopen_refuses_non_terminal(project_at_cwd, monkeypatch, status):
    """--reopen on non-terminal status errors with 'not terminal'."""
    from endless.task_cmd import spawn_plan

    monkeypatch.setenv("TMUX", "fake")

    _insert_task(
        pk=1500, project_id=project_at_cwd["project_id"],
        status=status, text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        spawn_plan(1500, reopen=True)
    msg = str(exc.value)
    assert "--reopen passed" in msg
    assert "not terminal" in msg


@pytest.mark.parametrize("status", ["declined", "obsolete"])
def test_spawn_reopen_refuses_declined_obsolete(project_at_cwd, monkeypatch, status):
    from endless.task_cmd import spawn_plan

    monkeypatch.setenv("TMUX", "fake")

    _insert_task(
        pk=1510, project_id=project_at_cwd["project_id"],
        status=status, text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        spawn_plan(1510, reopen=True)
    msg = str(exc.value)
    assert f"is '{status}'" in msg
    assert "task update" in msg


def test_spawn_no_flag_terminal_target_points_at_reopen(project_at_cwd, monkeypatch):
    """`task spawn E-X` (no flag) on assumed/confirmed/completed cites --reopen."""
    from endless.task_cmd import spawn_plan

    monkeypatch.setenv("TMUX", "fake")

    _insert_task(
        pk=1600, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        spawn_plan(1600)
    msg = str(exc.value)
    assert "assumed" in msg
    assert "--reopen" in msg
    assert "endless task reopen" in msg


def test_spawn_no_flag_unverified_keeps_force_error(project_at_cwd, monkeypatch):
    """`task spawn` on unverified still points at --force (not --reopen)."""
    from endless.task_cmd import spawn_plan

    monkeypatch.setenv("TMUX", "fake")

    _insert_task(
        pk=1610, project_id=project_at_cwd["project_id"],
        status="unverified", text="plan",
    )

    with pytest.raises(click.ClickException) as exc:
        spawn_plan(1610)
    msg = str(exc.value)
    assert "unverified" in msg
    assert "--force" in msg
    assert "--reopen" not in msg
