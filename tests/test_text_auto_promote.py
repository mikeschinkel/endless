"""Tests for E-1266: attaching a non-empty --text auto-promotes a
`needs_plan` task to `ready`. Applies on both `task add` and
`task update`. An explicit --status in the same call always wins.
"""

import pytest

from endless import task_cmd, db


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


# --- task add ---------------------------------------------------------------

def test_add_with_text_promotes_to_ready(tmp_path, seeded_project_at_cwd):
    plan = tmp_path / "plan.md"
    plan.write_text("# plan\nsome body\n")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        text_file=str(plan),
    )

    assert _status_of(item_id) == "ready"


def test_add_without_text_stays_needs_plan(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "needs_plan"


def test_add_with_empty_text_file_does_not_promote(tmp_path, seeded_project_at_cwd):
    """A whitespace-only plan file does not count as an attached plan."""
    plan = tmp_path / "empty.md"
    plan.write_text("   \n\n")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        text_file=str(plan),
    )
    assert _status_of(item_id) == "needs_plan"


def test_add_with_text_and_explicit_status_preserves_caller_status(tmp_path, seeded_project_at_cwd):
    """An explicit --status overrides the auto-promotion."""
    plan = tmp_path / "plan.md"
    plan.write_text("# plan")

    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        text_file=str(plan),
        status="blocked",
    )
    assert _status_of(item_id) == "blocked"


def test_add_tier_1_with_text_stays_ready(tmp_path, seeded_project_at_cwd):
    """Tier-1 already defaults to ready; the text path keeps it ready."""
    plan = tmp_path / "plan.md"
    plan.write_text("# plan")

    item_id = task_cmd.add_item(
        title="Add a quick thing",
        description="short",
        text_file=str(plan),
        tier=1,
    )
    assert _status_of(item_id) == "ready"


# --- task update ------------------------------------------------------------

def test_update_with_text_on_needs_plan_promotes_to_ready(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "needs_plan"

    plan = tmp_path / "plan.md"
    plan.write_text("# plan\nbody\n")
    task_cmd.update_plan(item_id=item_id, text_file=str(plan))

    assert _status_of(item_id) == "ready"


def test_update_with_text_on_ready_task_no_change(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
        tier=1,
    )
    assert _status_of(item_id) == "ready"

    plan = tmp_path / "plan.md"
    plan.write_text("# plan")
    task_cmd.update_plan(item_id=item_id, text_file=str(plan))

    assert _status_of(item_id) == "ready"


def test_update_with_text_plus_explicit_status_caller_wins(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "needs_plan"

    plan = tmp_path / "plan.md"
    plan.write_text("# plan")
    task_cmd.update_plan(item_id=item_id, text_file=str(plan), status="blocked")

    assert _status_of(item_id) == "blocked"


def test_update_with_empty_text_does_not_promote(tmp_path, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing",
        description="short",
    )
    assert _status_of(item_id) == "needs_plan"

    plan = tmp_path / "empty.md"
    plan.write_text("   \n")
    task_cmd.update_plan(item_id=item_id, text_file=str(plan))

    assert _status_of(item_id) == "needs_plan"
