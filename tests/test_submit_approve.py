"""Tests for E-1648: the `submit` (agent) and `approve` (human) verbs and
the approval gate they enforce.

Lifecycle: untriaged/unplanned → submitted → (approve) → ready. `submit` is the
description-sufficient path to `submitted` (the plan-attach path is covered
by test_text_auto_promote.py). `approve` is the human gate to `ready`.

E-1845 made `untriaged` the default `task add` status and added it to
_SUBMITTABLE_FROM, so `submit` is also the manual triage route out of it — the
guarantee that a freshly filed task is never stranded with no evaluator around.

Background-session gating (approve + non-ready pickup) is exercised
end-to-end by .endless/tasks/e-1648/verify.sh, which can seed a
`kind=background` session; these unit tests run with no resolvable session
(so never background) and cover the status-transition logic.
"""

import pytest
import click

from endless import task_cmd, db


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


# --- submit -----------------------------------------------------------------

@pytest.mark.parametrize("start", ["untriaged", "unplanned"])
def test_submit_pre_judgment_goes_to_submitted(start, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=start
    )
    assert _status_of(item_id) == start

    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"


def test_submit_is_the_manual_route_out_of_untriaged(seeded_project_at_cwd):
    """E-1845 no-deadlock guarantee: a task added with no --status lands in
    `untriaged` and can be moved out by `submit` alone, with no triager
    present. Without `untriaged` in _SUBMITTABLE_FROM every new task would be
    stranded until E-1859 ships."""
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "untriaged"

    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"


def test_submit_revisit_goes_to_submitted(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, status="revisit")
    assert _status_of(item_id) == "revisit"

    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"


def test_submit_already_submitted_is_noop(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"

    # Second submit is a friendly no-op, not an error, and does not change status.
    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"


def test_submit_from_ready_is_refused(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short", tier=1)
    assert _status_of(item_id) == "ready"

    with pytest.raises(click.ClickException):
        task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "ready"


# --- approve ----------------------------------------------------------------

def test_approve_submitted_goes_to_ready(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"

    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


@pytest.mark.parametrize("start", ["untriaged", "unplanned"])
def test_approve_pre_judgment_is_refused(start, seeded_project_at_cwd):
    """Approval is a gate on a SPEC; neither pre-judgment status has one yet."""
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=start
    )
    assert _status_of(item_id) == start

    with pytest.raises(click.ClickException):
        task_cmd.approve_item(item_id)
    assert _status_of(item_id) == start


def test_approve_already_ready_is_noop(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short", tier=1)
    assert _status_of(item_id) == "ready"

    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


def test_full_roundtrip_untriaged_submit_approve(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "untriaged"
    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"
    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"
