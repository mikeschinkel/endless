"""Tests for `endless task reopen` (E-1555, E-1889, E-1968).

Exercises reopen semantics from the E-1555 plan, as amended by E-1889 and
E-1968:
  - Reopen flips assumed/confirmed/completed → revisit, whatever the plan
    text says. Text presence survives only as the message suffix.
  - Reopen refuses on declined/obsolete (steers to `task update --status`).
  - Reopen refuses on non-terminal statuses.
  - Reopen LEAVES an existing session→task binding alone (E-1968). It used to
    clear it silently; ED-1560 makes that column write-once.
  - `task spawn --reopen` is retired (E-1968) and refuses with a pointer at
    `session goto --resume --revisit`.
  - `task spawn` (no flag) on a reopenable terminal target points at that same
    route rather than at a reopen-then-spawn dance.
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
    task_id: int | None = None,
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, task_id) "
        "VALUES (?, ?, ?, 'claude', ?, '2026-06-11T00:00:00', ?)",
        (pk, session_id, project_id, state, task_id),
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
                                    "unverified", "submitted", "revisit"])
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


def test_reopen_keeps_the_session_binding(project_at_cwd):
    """E-1968: reopen changes task state and nothing else.

    It used to emit `task.released` for whichever session held the task,
    clearing `task_id` with no mention in its output and a --help line
    ("no session binding") that read as "does not create one". That pointer is
    the only route back to the session's transcript — E-1917's was lost this
    way a week after landing, after which both resume paths reported the
    session had never claimed a task.
    """
    from endless.task_cmd import reopen_item

    _insert_task(
        pk=1200, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )
    _insert_session(
        pk=400, session_id="s-400",
        project_id=project_at_cwd["project_id"],
        task_id=1200,
    )

    reopen_item(1200)

    row = db.query(
        "SELECT task_id FROM sessions WHERE id = 400",
    )[0]
    assert row["task_id"] == 1200
    assert db.query(
        "SELECT status FROM tasks WHERE id = 1200"
    )[0]["status"] == "revisit"


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


# ---------- spawn --reopen is retired (E-1968) ----------


@pytest.mark.parametrize("flag", ["reopen", "new_session", "print_decision"])
def test_spawn_reopen_flags_are_retired_and_point_at_the_route(flag):
    """The flag still parses, and answers with where the capability went.

    Kept hidden-and-refusing (the `task start` precedent) rather than deleted so
    muscle memory gets a route instead of a click parse error. --new-session and
    --print-decision were --reopen-only modifiers and go with it.
    """
    from click.testing import CliRunner
    from endless.cli import task_cmd as task_group

    result = CliRunner().invoke(
        task_group, ["spawn", "E-1400", f"--{flag.replace('_', '-')}"]
    )
    assert result.exit_code != 0
    assert "retired" in result.output
    assert "endless session goto E-1400 --resume --revisit" in result.output


def test_spawn_plan_no_longer_accepts_reopen_kwargs():
    """The retirement reaches the callable, not just the CLI surface."""
    import inspect
    from endless.task_cmd import spawn_plan

    params = inspect.signature(spawn_plan).parameters
    for gone in ("reopen", "new_session", "print_decision"):
        assert gone not in params


def test_spawn_no_flag_terminal_target_routes_to_the_session(project_at_cwd, monkeypatch):
    """E-1968 §6: `task spawn E-X` on assumed/confirmed/completed no longer
    offers `task reopen E-X` first.

    That route was the trap: reopen moves the task to `revisit`, which is
    OUTSIDE _CLAIM_REQUIRES_FORCE, so the follow-up plain spawn proceeded with
    no prompt at all — the message walked the user around its own guard.
    """
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
    assert "endless session goto E-1600 --resume --revisit" in msg
    assert "--reopen" not in msg
    assert "endless task reopen" not in msg


# ---------- the background-session gate is unaffected by the revisit target ----


def test_any_session_can_claim_a_reopened_task(project_at_cwd):
    """E-1889 put `revisit` in the claim-promotion set, so a reopened task is
    claimable rather than a dead end.

    This replaces a PAIR of tests. The other pinned a second gate: a
    `kind=background` session was refused anything that was not human-approved
    `ready` work, so a reopen could not hand unapproved work to an unattended
    loop. E-2074 removed background agents, and with them the only session kind
    that gate could ever fire on — there is no unattended claimer left to
    refuse. What remains is this: `revisit` is claimable.
    """
    from endless.task_cmd import claim_item, reopen_item

    _insert_task(
        pk=1710, project_id=project_at_cwd["project_id"],
        status="assumed", text="plan",
    )
    reopen_item(1710)
    assert db.query(
        "SELECT status FROM tasks WHERE id = 1710"
    )[0]["status"] == "revisit"

    _insert_session(
        pk=510, session_id="s-510", project_id=project_at_cwd["project_id"],
    )

    with patch("endless.task_cmd._resolve_session_id_with_prompt",
               return_value=510):
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
    assert "--revisit" not in msg
