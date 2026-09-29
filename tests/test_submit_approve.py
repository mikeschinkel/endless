"""Tests for E-1648: the `submit` (agent) and `approve` (human) verbs and
the approval gate they enforce.

Lifecycle: unplanned → submitted → (approve) → ready. Attaching a plan moves
a task to `submitted` by itself (test_plan_auto_promote.py); `submit` is the
explicit route, for a plan attached with --keep-status or a `revisit` task
being re-submitted. E-1993: `submit` needs a plan — what is approved is the
plan, and a description is never a sufficient spec. So the tasks here are
filed with a plan attached without promotion (`_add`), unless the test is
about not having one.

Background-session gating (approve + non-ready pickup) is exercised
end-to-end by .endless/tasks/e-1648/verify.sh, which can seed a
`kind=background` session; these unit tests run with no resolvable session
(so never background) and cover the status-transition logic.
"""

import pytest
import click

from endless import task_cmd, db

# E-1813: submit proposes both ratings and approve ratifies them, so the happy
# paths below pass them; the refusals are pinned at the bottom of the file.
RATED = {"complexity": "low", "risk": "medium"}


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


def _add(**kw) -> int:
    """`task add`, then a plan attached without promotion (E-1993)."""
    item_id = task_cmd.add_item(**kw)
    task_cmd.update_plan(item_id=item_id, plan="# Plan\nbody\n", keep_status=True)
    return item_id


def _ratings_of(item_id: int) -> tuple:
    rows = db.query(
        "SELECT (SELECT slug FROM complexity_levels WHERE id = complexity_id) AS c, "
        "(SELECT slug FROM risk_levels WHERE id = risk_id) AS r "
        "FROM tasks WHERE id = ?", (item_id,))
    return rows[0]["c"], rows[0]["r"]


# --- submit -----------------------------------------------------------------

@pytest.mark.parametrize("start", ["unplanned"])
def test_submit_pre_judgment_goes_to_submitted(start, seeded_project_at_cwd):
    item_id = _add(
        title="Add a thing", description="short", status=start
    )
    assert _status_of(item_id) == start

    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"


def test_submit_without_a_plan_is_refused(seeded_project_at_cwd):
    """E-1993: there is no description-sufficient route any more."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"

    with pytest.raises(click.ClickException) as exc:
        task_cmd.submit_item(item_id, **RATED)
    assert "no plan" in str(exc.value)
    assert "--plan-file" in str(exc.value)
    assert _status_of(item_id) == "unplanned"


def test_update_status_submitted_without_a_plan_is_refused(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, status="submitted", **RATED)
    assert "no plan" in str(exc.value)
    assert _status_of(item_id) == "unplanned"


def test_submit_revisit_goes_to_submitted(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, status="revisit")
    assert _status_of(item_id) == "revisit"

    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"


def test_submit_already_submitted_is_noop(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"

    # Second submit is a friendly no-op, not an error, and does not change status.
    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"


def test_submit_from_ready_is_refused(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short", status="ready")
    assert _status_of(item_id) == "ready"

    with pytest.raises(click.ClickException):
        task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "ready"


# --- approve ----------------------------------------------------------------

def test_approve_submitted_goes_to_ready(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"

    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


@pytest.mark.parametrize("start", ["unplanned"])
def test_approve_pre_judgment_is_refused(start, seeded_project_at_cwd):
    """Approval is a gate on a SPEC; neither pre-judgment status has one yet."""
    item_id = _add(
        title="Add a thing", description="short", status=start
    )
    assert _status_of(item_id) == start

    with pytest.raises(click.ClickException):
        task_cmd.approve_item(item_id)
    assert _status_of(item_id) == start


def test_approve_already_ready_is_noop(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short", status="ready")
    assert _status_of(item_id) == "ready"

    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


def test_full_roundtrip_submit_approve(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"
    task_cmd.submit_item(item_id, **RATED)
    assert _status_of(item_id) == "submitted"
    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


# --- E-1813: the rating gates ------------------------------------------------

def test_submit_refuses_an_unrated_task(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.submit_item(item_id)

    msg = str(exc.value)
    assert "complexity and risk are unrated" in msg, msg
    assert "--complexity <low|medium|high> --risk <low|medium|high>" in msg, msg
    assert _status_of(item_id) == "unplanned", "the refusal changes nothing"
    assert _ratings_of(item_id) == (None, None)


def test_submit_names_only_the_missing_axis(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short",
                                complexity="high")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.submit_item(item_id)

    msg = str(exc.value)
    assert "risk is unrated" in msg and "--complexity" not in msg, msg


def test_submit_accepts_ratings_already_on_the_task(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short", **RATED)

    task_cmd.submit_item(item_id)

    assert _status_of(item_id) == "submitted"
    assert _ratings_of(item_id) == ("low", "medium")


def test_submit_records_the_proposed_ratings(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")

    task_cmd.submit_item(item_id, complexity="HIGH", risk="low")

    assert _ratings_of(item_id) == ("high", "low")


def test_resubmitting_with_new_ratings_updates_them(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.submit_item(item_id, **RATED)

    task_cmd.submit_item(item_id, risk="high")

    assert _status_of(item_id) == "submitted"
    assert _ratings_of(item_id) == ("low", "high")


def _submitted_unrated() -> int:
    """A submitted task with no ratings — what a plan-attach promotion leaves."""
    item_id = _add(title="Add a thing", description="short",
                                status="unplanned")
    task_cmd.update_plan(item_id=item_id, plan="# plan\n")
    assert _status_of(item_id) == "submitted"
    assert _ratings_of(item_id) == (None, None)
    return item_id


def test_approve_refuses_an_unrated_task(seeded_project_at_cwd):
    item_id = _submitted_unrated()

    with pytest.raises(click.ClickException) as exc:
        task_cmd.approve_item(item_id)

    assert "Cannot approve" in str(exc.value)
    assert _status_of(item_id) == "submitted"


def test_approve_can_supply_the_ratings_it_ratifies(capsys, seeded_project_at_cwd):
    item_id = _submitted_unrated()
    capsys.readouterr()

    task_cmd.approve_item(item_id, complexity="medium", risk="high")

    assert _status_of(item_id) == "ready"
    assert _ratings_of(item_id) == ("medium", "high")
    assert "Ratified: complexity medium, risk high" in capsys.readouterr().out


def test_approve_can_override_a_proposed_rating(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.submit_item(item_id, **RATED)

    task_cmd.approve_item(item_id, risk="low")

    assert _ratings_of(item_id) == ("low", "low")


def test_approve_refuses_when_an_override_clears_a_rating(seeded_project_at_cwd):
    item_id = _add(title="Add a thing", description="short")
    task_cmd.submit_item(item_id, **RATED)

    with pytest.raises(click.ClickException):
        task_cmd.approve_item(item_id, complexity="none")
    assert _status_of(item_id) == "submitted"
    assert _ratings_of(item_id) == ("low", "medium"), "nothing is written"


@pytest.mark.parametrize("status", ["submitted", "ready"])
def test_update_status_meets_the_same_gate(status, seeded_project_at_cwd):
    """`task update --status submitted|ready` is submit/approve by another verb;
    left ungated, either gate would be one command away from meaningless."""
    item_id = (
        _add(title="Add a thing", description="short")
        if status == "submitted" else _submitted_unrated()
    )
    before = _status_of(item_id)

    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, status=status)
    assert _status_of(item_id) == before

    task_cmd.update_plan(item_id=item_id, status=status, **RATED)
    assert _status_of(item_id) == status


def test_claiming_needs_no_rating(seeded_project_at_cwd):
    """Ratings gate approval, never work: an unrated task can still go
    `underway`, the edge `task claim` takes."""
    item_id = _add(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, status="underway")
    assert _status_of(item_id) == "underway"


def test_an_illegal_edge_is_refused_by_the_lifecycle_not_the_rating_gate(
    seeded_project_at_cwd
):
    """`--status ready` on an unplanned task is not an approval at all; saying
    "unrated" would send the caller to fix the wrong thing."""
    item_id = _add(title="Add a thing", description="short")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(item_id=item_id, status="ready")

    assert "unrated" not in str(exc.value), exc.value
    assert "not a legal status change" in str(exc.value), exc.value
