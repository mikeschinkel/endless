"""Tests for `endless task reopen` and `task spawn --reopen` (E-1555, E-1889).

Exercises reopen semantics from the E-1555 plan, as amended by E-1889:
  - Reopen flips assumed/confirmed/completed → revisit, whatever the plan
    text says. Text presence survives only as the message suffix.
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


@pytest.mark.parametrize("status", ["assumed", "confirmed", "completed"])
def test_reopen_lands_revisit_from_every_reopenable_status(
    project_at_cwd, capsys, status,
):
    """E-1889: all three reopenable statuses land `revisit`, plan or no plan."""
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1000, project_id=project_at_cwd["project_id"],
        status=status, text="# plan body\n",
    )

    reopen_item(1000)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (1000,))[0]
    assert row["status"] == "revisit"

    captured = capsys.readouterr()
    assert "Updated E-1000" in captured.out
    assert f"{status} -> revisit" in captured.out


def test_reopen_without_text_still_lands_revisit(project_at_cwd, capsys):
    """Text presence no longer branches the target status (E-1889) — it only
    survives as the message suffix, which still reports it honestly."""
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1001, project_id=project_at_cwd["project_id"],
        status="confirmed", text=None,
    )

    reopen_item(1001)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (1001,))[0]
    assert row["status"] == "revisit"

    captured = capsys.readouterr()
    assert "confirmed -> revisit" in captured.out
    assert "text: absent" in captured.out


def test_reopen_with_text_reports_the_plan_in_the_suffix(project_at_cwd, capsys):
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1004, project_id=project_at_cwd["project_id"],
        status="assumed", text="# plan body\n",
    )

    reopen_item(1004)

    captured = capsys.readouterr()
    assert "text: present" in captured.out


def test_reopen_completed_epic_does_not_cascade(project_at_cwd, capsys):
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
    assert parent["status"] == "revisit"
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


# ---------- the background-session gate is unaffected by the revisit target ----


def test_background_session_cannot_claim_a_reopened_task(project_at_cwd):
    """E-1889 put `revisit` in the claim-promotion set, which is the *hook*
    path. The background-session gate is a separate check and must still
    refuse anything that is not human-approved `ready` work — otherwise a
    reopen would become a way to hand unapproved work to a background loop.
    """
    from endless.task_cmd import claim_item, reopen_item

    _insert_task(
        pk=1700, project_id=project_at_cwd["project_id"],
        status="confirmed", text="plan",
    )
    reopen_item(1700)
    assert db.query(
        "SELECT status FROM tasks WHERE id = 1700"
    )[0]["status"] == "revisit"

    _insert_session(
        pk=500, session_id="s-500", project_id=project_at_cwd["project_id"],
    )

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=500), \
         patch("endless.task_cmd._session_is_background", return_value=True):
        with pytest.raises(click.ClickException) as exc:
            claim_item(1700)

    msg = str(exc.value)
    assert "background session may only claim 'ready' work" in msg
    assert "'revisit'" in msg
    assert db.query(
        "SELECT status FROM tasks WHERE id = 1700"
    )[0]["status"] == "revisit"


def test_foreground_session_can_claim_a_reopened_task(project_at_cwd):
    """The counterpart: an ordinary session picks the reopened task straight
    up — `revisit` is not a dead end."""
    from endless.task_cmd import claim_item, reopen_item

    _insert_task(
        pk=1710, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )
    reopen_item(1710)

    _insert_session(
        pk=510, session_id="s-510", project_id=project_at_cwd["project_id"],
    )

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=510), \
         patch("endless.task_cmd._session_is_background", return_value=False):
        claim_item(1710)

    assert db.query(
        "SELECT status FROM tasks WHERE id = 1710"
    )[0]["status"] == "underway"


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
