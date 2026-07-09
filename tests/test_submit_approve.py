"""Tests for E-1648: the `submit` (agent) and `approve` (human) verbs and
the approval gate they enforce.

Lifecycle: unplanned → submitted → (approve) → ready. `submit` is the
description-sufficient path to `submitted` (the plan-attach path is covered
by test_text_auto_promote.py). `approve` is the human gate to `ready`.

Background-session gating (approve + non-ready pickup) is exercised
end-to-end by tests/tasks/e-1648-verify.sh, which can seed a
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

def test_submit_unplanned_goes_to_submitted(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"

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


def test_approve_unplanned_is_refused(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"

    with pytest.raises(click.ClickException):
        task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "unplanned"


def test_approve_already_ready_is_noop(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short", tier=1)
    assert _status_of(item_id) == "ready"

    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"


def test_full_roundtrip_unplanned_submit_approve(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"
    task_cmd.submit_item(item_id)
    assert _status_of(item_id) == "submitted"
    task_cmd.approve_item(item_id)
    assert _status_of(item_id) == "ready"
