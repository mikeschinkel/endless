"""Tests for E-1913: `--keep-status` holds the status across EVERY auto-transition.

`task update` infers a status change from what you edited, in three places:

  1. non-empty `--plan` on a pre-judgment task      -> `submitted`  (E-1266/E-1648)
  2. a material `--plan` edit on a `ready` task     -> `submitted`  (E-1993)
  3. `--tier 1` on a pre-judgment task              -> `ready`   (removed, E-1813)

Leg 2 was guarded by the flag from the start (it replaced E-1845's
description-edit reset, which was too). Leg 1 leaked: the promotion lives in the
Go executor, and the flag had no way to cross the Python-to-executor boundary —
so appending a line to an `unplanned` task's plan silently promoted it, which is
how E-1671 lost a deliberately-unapproved status. Leg 3 was never guarded
either; E-1813 removed it outright, and its replacement — a complexity/risk
rating — infers no status at all.

There was a fourth: a real `--plan` edit on a done task inferred `revisit`
(E-1762). E-2120 removed the inference rather than the guard, so that edit is
now inert with the flag or without it; the last section here holds what remains
true about it.

The flag now means exactly what its name says: the status you see is the status
you keep. Its siblings — the no-op-on-identical-rewrite guard and the
"explicit --status wins" rule — are covered in test_plan_required_status_model.py and
test_plan_auto_promote.py; what is asserted here is the flag itself.
"""

import click
import pytest

from endless import db, task_cmd


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


def _finish(item_id: int, status: str, **kwargs) -> None:
    """Walk a task the legal way to a verification-track status.

    Setting one directly on a `submitted` task used to work and no longer does:
    E-2018 made `task update --status` enforce the lifecycle, and a status that
    reports work is refused on a task no work was done on. These tests want a
    task IN a done state, so they walk it there rather than asserting it.
    """
    # E-1813: approving ratifies both ratings, so the walk supplies them.
    task_cmd.update_plan(item_id=item_id, status="ready",
                         complexity="low", risk="low")
    for step in ("underway", "unverified"):
        task_cmd.update_plan(item_id=item_id, status=step)
    task_cmd.update_plan(item_id=item_id, status=status, **kwargs)


def _approve(item_id: int) -> None:
    """Take an unrated pre-work task to `ready` the legal way: submit with
    ratings, then approve (E-1813 removed the tier-1 shortcut)."""
    task_cmd.update_plan(item_id=item_id, status="submitted",
                         complexity="low", risk="low")
    task_cmd.update_plan(item_id=item_id, status="ready")


def _row(item_id: int) -> dict:
    rows = db.query(
        "SELECT status, (SELECT content FROM task_content WHERE task_id = tasks.id AND name = 'plan') AS plan, complexity_id, completed_at FROM tasks WHERE id = ?",
        (item_id,),
    )
    assert rows, f"task E-{item_id} not found"
    return rows[0]


# --- leg 1: the plan-attach promotion (the gap E-1913 closes) ----------------

@pytest.mark.parametrize("start", ["unplanned"])
def test_keep_status_suppresses_the_plan_attach_promotion(
    start, seeded_project_at_cwd
):
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=start
    )
    assert _status_of(item_id) == start

    task_cmd.update_plan(item_id=item_id, plan="# plan\nbody\n", keep_status=True)

    row = _row(item_id)
    assert row["status"] == start, "the promotion must not fire"
    assert row["plan"] == "# plan\nbody\n", "the plan must still be written"


@pytest.mark.parametrize("start", ["unplanned"])
def test_without_the_flag_the_promotion_still_fires(start, seeded_project_at_cwd):
    """The promotion is correct default behavior — suppressing it is opt-in."""
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=start
    )

    task_cmd.update_plan(item_id=item_id, plan="# plan\nbody\n")

    assert _status_of(item_id) == "submitted"


def test_keep_status_holds_an_append_to_an_existing_plan(seeded_project_at_cwd):
    """The E-1671 case: a task parked at `unplanned` gains a finding, not a status."""
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status="unplanned"
    )
    task_cmd.update_plan(item_id=item_id, plan="# plan\n", keep_status=True)

    task_cmd.update_plan(
        item_id=item_id, plan="# plan\n\n## Finding\nnew\n", keep_status=True
    )

    row = _row(item_id)
    assert row["status"] == "unplanned"
    assert "## Finding" in row["plan"]


# --- leg 2: the plan-edit reset on an approved task (E-1993) -----------------

def test_keep_status_suppresses_the_plan_reset(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short", plan="# plan\n")
    _approve(item_id)

    task_cmd.update_plan(item_id=item_id, plan="# Plan.\n", keep_status=True)

    assert _status_of(item_id) == "ready"


# --- the retired leg: a plan edit on a done task (E-1762 -> E-2120) ----------

def test_a_plan_edit_on_a_done_task_infers_nothing(seeded_project_at_cwd):
    """The inference is gone, so the flag is not what holds the status here.

    E-1762 read a real `--plan` change on a finished task as unshipped scope and
    flipped it to `revisit`. Recording what shipped is now an obligation on any
    session that folds discovered work into the task it is on, so that edit is
    routine — and `revisit` has no edge back to `assumed`, so the flip destroyed
    a verification the user had granted, to report an edit the session was told
    to make.
    """
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, plan="# plan\n")
    _finish(item_id, "assumed")

    task_cmd.update_plan(item_id=item_id, plan="# plan\n\n## Also shipped\nx\n")

    row = _row(item_id)
    assert row["status"] == "assumed", "no status is inferred from the edit"
    assert "## Also shipped" in row["plan"], "the plan edit still lands"


def test_a_plan_edit_on_a_done_task_reports_no_status_change(
    capsys, seeded_project_at_cwd
):
    """Nothing moved, so nothing about status is printed — to either audience."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, plan="# plan\n")
    _finish(item_id, "assumed")
    capsys.readouterr()

    task_cmd.update_plan(item_id=item_id, plan="# plan\n\n## Also shipped\nx\n")

    out = capsys.readouterr().out
    assert "Status:" not in out, out
    assert "revisit" not in out, out


def test_keep_status_on_a_done_task_still_holds(seeded_project_at_cwd):
    """The flag is now redundant here, not wrong: it is what the discovery
    rules tell a session to pass, and it must stay a no-op rather than an
    error or a surprise."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, plan="# plan\n")
    _finish(item_id, "assumed")

    task_cmd.update_plan(item_id=item_id, plan="# plan (typo fixed)\n", keep_status=True)

    assert _status_of(item_id) == "assumed"


def test_keep_status_on_a_done_task_does_not_restamp_completed_at(
    seeded_project_at_cwd
):
    """The pin is scoped to the case that needs it.

    Suppressing leg 1 works by sending the current status, which the executor's
    "caller wins" branch honors. But a status field is not inert there: whenever
    one is present the executor also rewrites `completed_at`. Pinning unconditionally would therefore restamp
    the completion time of a `confirmed` task whose plan was merely
    typo-fixed — so the pin fires only where the promotion would have.
    """
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, plan="# plan\n")
    _finish(item_id, "confirmed", outcome="shipped")
    before = _row(item_id)["completed_at"]
    assert before, "fixture: a confirmed task carries a completion timestamp"

    task_cmd.update_plan(item_id=item_id, plan="# plan (typo fixed)\n", keep_status=True)

    after = _row(item_id)
    assert after["status"] == "confirmed"
    assert after["completed_at"] == before, "the completion time is history"


# --- leg 3, retired: a rating infers no status (E-1813) ----------------------

@pytest.mark.parametrize("start", ["unplanned"])
@pytest.mark.parametrize("keep_status", [False, True])
def test_a_rating_edit_infers_no_status(start, keep_status, seeded_project_at_cwd):
    """Tier 1 advanced a pre-work task to `ready`. Its replacement must not:
    status routing is the plan plus approve, never a rating value."""
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=start
    )

    task_cmd.update_plan(item_id=item_id, complexity="low", risk="low",
                         keep_status=keep_status)

    row = _row(item_id)
    assert row["status"] == start, "a rating must not move status"
    assert row["complexity_id"] == 1, "the rating must still be set"


# --- the flag contradicts an explicit --status ------------------------------

def test_status_together_with_keep_status_is_refused(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, status="ready", keep_status=True)

    assert "contradict" in str(exc.value)
    assert _status_of(item_id) == "unplanned", "the refusal changes nothing"


def test_the_refusal_precedes_every_other_effect(seeded_project_at_cwd):
    """Refused before the row is even loaded — a bad id is not what it reports."""
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=999999, status="ready", keep_status=True)

    assert "contradict" in str(exc.value)


# --- what the flag does NOT do ----------------------------------------------

def test_keep_status_still_writes_every_other_field(seeded_project_at_cwd):
    """It holds the status. It is not a dry run."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")

    task_cmd.update_plan(
        item_id=item_id,
        title="Add a renamed thing",
        description="a materially different spec",
        plan="# plan\n",
        phase="later",
        keep_status=True,
    )

    rows = db.query(
        "SELECT title, description, (SELECT content FROM task_content WHERE task_id = tasks.id AND name = 'plan') AS plan, phase, status FROM tasks WHERE id = ?",
        (item_id,),
    )
    assert rows[0]["title"] == "Add a renamed thing"
    assert rows[0]["description"] == "a materially different spec"
    assert rows[0]["plan"] == "# plan\n"
    assert rows[0]["phase"] == "later"
    assert rows[0]["status"] == "unplanned"


def test_keep_status_does_not_report_a_status_change(capsys, seeded_project_at_cwd):
    """The pinned write is a no-op write; rendering it would misreport the update."""
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status="unplanned"
    )

    task_cmd.update_plan(item_id=item_id, plan="# plan\n", keep_status=True)

    out = capsys.readouterr().out
    assert "Status:" not in out, out
