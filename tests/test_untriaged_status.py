"""Tests for E-1845: the `untriaged` status upstream of `unplanned`.

`untriaged` means "filed, but nobody has looked at it yet". Every new task
starts there; triage routes it to `submitted` (the description is already a
sufficient spec) or `unplanned` (design work needed first).

Covered here:
  - `task add` defaults to `untriaged`; `--tier 1` and an explicit `--status`
    still win.
  - No deadlock: a freshly filed task can leave `untriaged` with no triager
    present (the `submit` route lives in test_submit_approve.py; the
    plan-attach route in test_plan_auto_promote.py).
  - A material description edit resets a pre-work task to `untriaged`, with
    its three guards (no-op on identical description, `--keep-status`, and the
    excluded statuses).
  - `untriaged` is not offered by `task next`, but still blocks dependents.
"""

import click
import pytest

from endless import cli, db, task_cmd


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


def _set_status(item_id: int, status: str) -> None:
    """Force a status without going through the reset logic under test.

    A raw write, deliberately. What these tests need is a task sitting IN a
    status; how it got there is not their subject, and routing the setup through
    `task update --status` would make them assert the lifecycle guard (E-2018)
    as a side effect — a task in `underway` cannot be reached from `untriaged`
    by declaration, only by claiming it.
    """
    db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, item_id))
    assert _status_of(item_id) == status


# --- the default ------------------------------------------------------------

def test_add_defaults_to_untriaged(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "untriaged"


def test_add_tier_1_still_lands_ready(seeded_project_at_cwd):
    """A tier-1 task is exempt from planning, so it is exempt from triage."""
    item_id = task_cmd.add_item(
        title="Add a quick thing", description="short", tier=1
    )
    assert _status_of(item_id) == "ready"


@pytest.mark.parametrize("status", ["unplanned", "ready", "submitted", "revisit"])
def test_add_explicit_status_wins(status, seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", status=status
    )
    assert _status_of(item_id) == status


def test_setting_tier_1_later_advances_untriaged_to_ready(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "untriaged"

    task_cmd.update_plan(item_id=item_id, tier=1)
    assert _status_of(item_id) == "ready"


def test_untriaged_is_a_registered_status(seeded_project_at_cwd):
    """It must be selectable everywhere a status is: the click.Choice option
    types are all built from TASK_STATUSES, and `update --status` validates
    against its own tuple."""
    assert "untriaged" in cli.TASK_STATUSES

    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, status="unplanned")
    task_cmd.update_plan(item_id=item_id, status="untriaged")
    assert _status_of(item_id) == "untriaged"


def test_epic_and_research_may_be_untriaged(seeded_project_at_cwd):
    """An epic or research task filed from a one-line description needs
    triage as much as a todo does, so neither type forbids the status."""
    for task_type in ("epic", "research"):
        item_id = task_cmd.add_item(
            title="Explore a thing",
            description="short",
            task_type=task_type,
            justification="needed" if task_type == "research" else None,
        )
        assert _status_of(item_id) == "untriaged"


# --- description-edit reset -------------------------------------------------

_RESET_FROM = ["untriaged", "unplanned", "submitted", "ready", "revisit"]
_NO_RESET_FROM = ["underway", "unverified", "confirmed", "assumed", "obsolete"]


@pytest.mark.parametrize("start", _RESET_FROM)
def test_description_edit_resets_pre_work_statuses(start, seeded_project_at_cwd):
    """The description IS the spec triage and approval were judged against, so
    rewriting it invalidates those judgments."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, start)

    task_cmd.update_plan(item_id=item_id, description="materially different")
    assert _status_of(item_id) == "untriaged"


@pytest.mark.parametrize("start", _NO_RESET_FROM)
def test_description_edit_does_not_reset_work_statuses(start, seeded_project_at_cwd):
    """`underway` above all: a description tweak must never yank work out from
    under a live session."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, start)

    task_cmd.update_plan(item_id=item_id, description="materially different")
    assert _status_of(item_id) == start


def test_identical_description_rewrite_is_a_noop(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, "ready")

    task_cmd.update_plan(item_id=item_id, description="original")
    assert _status_of(item_id) == "ready"


def test_keep_status_suppresses_the_reset(seeded_project_at_cwd):
    """The escape hatch for a typo- or formatting-only edit."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, "ready")

    task_cmd.update_plan(
        item_id=item_id, description="Original.", keep_status=True
    )
    assert _status_of(item_id) == "ready"
    rows = db.query("SELECT description FROM tasks WHERE id = ?", (item_id,))
    assert rows[0]["description"] == "Original."


def test_explicit_status_wins_over_the_reset(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, "ready")

    task_cmd.update_plan(
        item_id=item_id, description="materially different", status="revisit"
    )
    assert _status_of(item_id) == "revisit"


def test_title_only_edit_does_not_reset(seeded_project_at_cwd):
    """Only the description carries the spec; renaming is not re-speccing."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, "ready")

    task_cmd.update_plan(item_id=item_id, title="Add a renamed thing")
    assert _status_of(item_id) == "ready"


def test_description_edit_with_plan_text_lands_submitted(seeded_project_at_cwd):
    """Rewriting the description AND attaching a plan in one call re-specs the
    task and answers the triage question at once, so the two rules compose to
    `submitted` — bouncing to `untriaged` would read as "nobody has looked at
    this" about a task carrying a full plan."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    assert _status_of(item_id) == "untriaged"

    task_cmd.update_plan(
        item_id=item_id, description="materially different", plan="# plan\nbody\n"
    )
    assert _status_of(item_id) == "submitted"


def test_description_edit_with_plan_text_still_costs_ready_its_approval(
    seeded_project_at_cwd
):
    """Approval was granted against the OLD description, so a re-spec drops a
    `ready` task back to `submitted` for re-approval — it does not stay ready
    just because a plan came along with the rewrite."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, "ready")

    task_cmd.update_plan(
        item_id=item_id, description="materially different", plan="# plan\nbody\n"
    )
    assert _status_of(item_id) == "submitted"


# --- not actionable, but still blocking -------------------------------------

def test_task_next_omits_untriaged(seeded_project_at_cwd, capsys):
    """A task nobody has looked at yet is not work you can pick up."""
    untriaged = task_cmd.add_item(title="Add an unlooked-at thing", description="d")
    ready = task_cmd.add_item(
        title="Add a ready thing", description="d", status="ready"
    )
    assert _status_of(untriaged) == "untriaged"

    capsys.readouterr()
    task_cmd.next_tasks(as_json=True)
    out = capsys.readouterr().out

    assert f"E-{ready}" in out
    assert f"E-{untriaged}" not in out


def test_untriaged_still_blocks_dependents(seeded_project_at_cwd, capsys):
    """Unfinished work is unfinished regardless of whether it has been looked
    at, so an untriaged blocker keeps its dependent off the actionable list."""
    blocker = task_cmd.add_item(title="Add a blocker", description="d")
    dependent = task_cmd.add_item(
        title="Add a dependent", description="d", status="ready"
    )
    task_cmd.link_tasks(blocker, dependent, "blocks")
    assert _status_of(blocker) == "untriaged"

    capsys.readouterr()
    task_cmd.next_tasks(as_json=True)
    out = capsys.readouterr().out

    assert f"E-{dependent}" not in out
