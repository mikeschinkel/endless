"""Tests for E-1993's status model: no `untriaged`, and the plan is the spec.

`untriaged` (E-1845) held a new task until triage judged whether its
description was a sufficient spec. A task now needs a plan before it can be
claimed or spawned, so no description ever is, and the status went with the
judgment.

Covered here:
  - `task add` files `unplanned`, or `submitted` with a plan; `--tier 1` and an
    explicit `--status` still win.
  - `untriaged` is not a status: it is refused, not stored.
  - A description edit never changes status, from any status.
  - A material plan edit on a `ready` task drops its approval (→ `submitted`),
    with its guards: no-op on an identical or whitespace-only rewrite,
    `--keep-status`, an explicit `--status`, and only from `ready`.
"""

import click
import pytest

from endless import cli, db, task_cmd


def _status_of(item_id: int) -> str:
    rows = db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))
    assert rows, f"task E-{item_id} not found"
    return rows[0]["status"]


def _set_status(item_id: int, status: str) -> None:
    """Force a status without going through the logic under test.

    A raw write, deliberately: these tests need a task sitting IN a status, and
    routing the setup through `task update --status` would assert the
    lifecycle guard (E-2018) as a side effect.
    """
    db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, item_id))
    assert _status_of(item_id) == status


def _plan_of(item_id: int) -> str | None:
    return db.task_content(item_id).get("plan")


# --- the entry statuses -----------------------------------------------------

def test_add_defaults_to_unplanned(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    assert _status_of(item_id) == "unplanned"


def test_add_with_a_plan_lands_submitted(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Add a thing", description="short", plan="# Plan\nbody\n"
    )
    assert _status_of(item_id) == "submitted"


def test_add_tier_1_still_lands_ready(seeded_project_at_cwd):
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


def test_setting_tier_1_later_advances_unplanned_to_ready(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Add a thing", description="short")
    task_cmd.update_plan(item_id=item_id, tier=1)
    assert _status_of(item_id) == "ready"


# --- untriaged is gone ------------------------------------------------------

def test_untriaged_is_not_a_status(seeded_project_at_cwd):
    assert "untriaged" not in cli.TASK_STATUSES

    item_id = task_cmd.add_item(title="Add a thing", description="short")
    with pytest.raises(click.ClickException):
        task_cmd.update_plan(item_id=item_id, status="untriaged")
    assert _status_of(item_id) == "unplanned"


# --- a description edit never changes status --------------------------------

_EVERY_STATUS = [
    "unplanned", "submitted", "ready", "revisit", "underway",
    "unverified", "confirmed", "assumed", "obsolete",
]


@pytest.mark.parametrize("start", _EVERY_STATUS)
def test_description_edit_never_changes_status(start, seeded_project_at_cwd):
    """The description describes the task; it is not the spec."""
    item_id = task_cmd.add_item(title="Add a thing", description="original")
    _set_status(item_id, start)

    task_cmd.update_plan(item_id=item_id, description="materially different")
    assert _status_of(item_id) == start


# --- a material plan edit on an approved task drops its approval -----------

def _ready_with_plan(plan: str = "# Plan\noriginal\n") -> int:
    item_id = task_cmd.add_item(title="Add a thing", description="d", plan=plan)
    _set_status(item_id, "ready")
    return item_id


def test_plan_edit_on_ready_returns_it_to_submitted(seeded_project_at_cwd):
    """What was approved was the plan, so changing it takes the approval back."""
    item_id = _ready_with_plan()
    task_cmd.update_plan(item_id=item_id, plan="# Plan\nsomething else\n")
    assert _status_of(item_id) == "submitted"
    assert "something else" in _plan_of(item_id)


def test_identical_plan_rewrite_keeps_approval(seeded_project_at_cwd):
    item_id = _ready_with_plan()
    task_cmd.update_plan(item_id=item_id, plan="# Plan\noriginal\n")
    assert _status_of(item_id) == "ready"


def test_whitespace_only_plan_change_keeps_approval(seeded_project_at_cwd):
    item_id = _ready_with_plan()
    task_cmd.update_plan(item_id=item_id, plan="\n# Plan\noriginal\n\n\n")
    assert _status_of(item_id) == "ready"


def test_keep_status_suppresses_the_plan_reset(seeded_project_at_cwd):
    item_id = _ready_with_plan()
    task_cmd.update_plan(
        item_id=item_id, plan="# Plan\ntypo fixd\n", keep_status=True
    )
    assert _status_of(item_id) == "ready"
    assert "typo fixd" in _plan_of(item_id)


def test_explicit_status_wins_over_the_plan_reset(seeded_project_at_cwd):
    item_id = _ready_with_plan()
    task_cmd.update_plan(
        item_id=item_id, plan="# Plan\nsomething else\n", status="revisit"
    )
    assert _status_of(item_id) == "revisit"


@pytest.mark.parametrize("start", ["underway", "unverified", "confirmed", "revisit"])
def test_plan_edit_after_ready_infers_nothing(start, seeded_project_at_cwd):
    """From `underway` on, a plan edit records what the work became."""
    item_id = _ready_with_plan()
    _set_status(item_id, start)
    task_cmd.update_plan(item_id=item_id, plan="# Plan\ngrown scope\n")
    assert _status_of(item_id) == start


def test_plan_edit_with_description_edit_still_resets_once(seeded_project_at_cwd):
    item_id = _ready_with_plan()
    task_cmd.update_plan(
        item_id=item_id, description="new words", plan="# Plan\nnew plan\n"
    )
    assert _status_of(item_id) == "submitted"


# --- not actionable, but still blocking -------------------------------------

def test_task_next_omits_submitted(seeded_project_at_cwd, capsys):
    """A plan awaiting approval is not work you can pick up."""
    submitted = task_cmd.add_item(
        title="Add a planned thing", description="d", plan="# Plan\n"
    )
    ready = task_cmd.add_item(
        title="Add a ready thing", description="d", status="ready"
    )
    assert _status_of(submitted) == "submitted"

    capsys.readouterr()
    task_cmd.next_tasks(as_json=True)
    out = capsys.readouterr().out

    assert f"E-{ready}" in out
    assert f"E-{submitted}" not in out


def test_unplanned_still_blocks_dependents(seeded_project_at_cwd, capsys):
    blocker = task_cmd.add_item(title="Add a blocker", description="d")
    dependent = task_cmd.add_item(
        title="Add a dependent", description="d", status="ready"
    )
    task_cmd.link_tasks(blocker, dependent, "blocks")
    assert _status_of(blocker) == "unplanned"

    capsys.readouterr()
    task_cmd.next_tasks(as_json=True)
    out = capsys.readouterr().out

    assert f"E-{dependent}" not in out
