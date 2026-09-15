"""Tests for E-1266 / E-1648: attaching a non-empty --plan moves a pre-judgment
task to `submitted` (spec-complete, awaiting human approval — NOT `ready`, which
now means human-approved). Applies on both `task add` and `task update`. An
explicit --status in the same call always wins.

E-1845 made `untriaged` the default `task add` status and added it alongside
`unplanned` as a promotion source, so the auto-move is exercised from both.
"""

import pytest

from endless import task_cmd, db


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


# --- task add ---------------------------------------------------------------

def test_add_with_plan_promotes_to_submitted(tmp_path, seeded_project_at_cwd):
    plan = tmp_path / "plan.md"
    plan.write_text("# plan\nsome body\n")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        plan=plan.read_text(),
    )

    assert _status_of(item_id) == "submitted"


def test_add_without_plan_stays_untriaged(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "untriaged"


def test_add_with_empty_plan_file_does_not_promote(tmp_path, seeded_project_at_cwd):
    """A whitespace-only plan file does not count as an attached plan."""
    plan = tmp_path / "empty.md"
    plan.write_text("   \n\n")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        plan=plan.read_text(),
    )
    assert _status_of(item_id) == "untriaged"


def test_add_with_plan_and_explicit_status_preserves_caller_status(tmp_path, seeded_project_at_cwd):
    """An explicit --status overrides the auto-promotion."""
    plan = tmp_path / "plan.md"
    plan.write_text("# plan")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        plan=plan.read_text(),
        status="ready",
    )
    assert _status_of(item_id) == "ready"


def test_add_tier_1_with_plan_stays_ready(tmp_path, seeded_project_at_cwd):
    """Tier-1 already defaults to ready; the plan path keeps it ready."""
    plan = tmp_path / "plan.md"
    plan.write_text("# plan")

    item_id = task_cmd.add_item(
        title="Add a quick thing",
        description="short",
        plan=plan.read_text(),
        tier=1,
    )
    assert _status_of(item_id) == "ready"


# --- task update ------------------------------------------------------------

@pytest.mark.parametrize("start", ["untriaged", "unplanned"])
def test_update_with_plan_on_pre_judgment_promotes_to_submitted(
    start, tmp_path, seeded_project_at_cwd
):
    """Both pre-judgment statuses promote — E-1845 added `untriaged`."""
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        status=start,
    )
    assert _status_of(item_id) == start

    plan = tmp_path / "plan.md"
    plan.write_text("# plan\nbody\n")
    task_cmd.update_plan(item_id=item_id, plan=plan.read_text())

    assert _status_of(item_id) == "submitted"


def test_update_with_plan_on_ready_task_no_change(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        tier=1,
    )
    assert _status_of(item_id) == "ready"

    plan = tmp_path / "plan.md"
    plan.write_text("# plan")
    task_cmd.update_plan(item_id=item_id, plan=plan.read_text())

    assert _status_of(item_id) == "ready"


def test_update_with_plan_plus_explicit_status_caller_wins(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "untriaged"

    plan = tmp_path / "plan.md"
    plan.write_text("# plan")
    task_cmd.update_plan(item_id=item_id, plan=plan.read_text(), status="ready")

    assert _status_of(item_id) == "ready"


def test_update_with_empty_plan_does_not_promote(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "untriaged"

    plan = tmp_path / "empty.md"
    plan.write_text("   \n")
    task_cmd.update_plan(item_id=item_id, plan=plan.read_text())

    assert _status_of(item_id) == "untriaged"
