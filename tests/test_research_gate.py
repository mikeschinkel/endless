"""Tests for E-1544: research-creation gate with parent-epic exemption
and --justification requirement (per ED-1504).

The gate logic lives in task_cmd._research_gate_check; storage shape in
task_cmd._compose_justification_notes. Both are exercised here at the
function level for granular coverage, plus end-to-end via task_cmd.add_item
and task_cmd.update_plan against a real isolated DB.
"""

import click
import pytest

from endless import db, task_cmd


def _add_task(
    title: str,
    status: str = "ready",
    task_type: str = "task",
    parent_id: int | None = None,
    notes: str | None = None,
) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, "
        "parent_id, notes, created_at) "
        "VALUES (1, ?, ?, (SELECT id FROM task_types WHERE slug = ?), "
        "'now', ?, ?, datetime('now'))",
        (title, status, task_type, parent_id, notes),
    )
    return cur.lastrowid


def _notes_and_type(task_id: int) -> tuple[str | None, str]:
    row = db.query(
        "SELECT notes, "
        "COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type "
        "FROM tasks WHERE id = ?",
        (task_id,),
    )
    return row[0]["notes"], row[0]["type"]


# ---------- helper-level unit tests ----------

def test_gate_passes_when_justification_provided(seeded_project_at_cwd):
    task_cmd._research_gate_check(None, "Why inline won't work.")  # no raise


def test_gate_passes_when_parent_is_epic_in_progress(seeded_project_at_cwd):
    epic = _add_task("Epic", status="in_progress", task_type="epic")
    task_cmd._research_gate_check(epic, None)  # no raise


def test_gate_refuses_when_no_parent_and_no_justification(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(None, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_epic_but_not_in_progress(seeded_project_at_cwd):
    epic = _add_task("Epic", status="ready", task_type="epic")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(epic, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_non_epic(seeded_project_at_cwd):
    parent = _add_task("Plain task", status="in_progress", task_type="task")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(parent, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_not_found(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(99999, None)
    assert "not found" in exc.value.message


def test_compose_notes_empty_existing_returns_section():
    out = task_cmd._compose_justification_notes(None, "Needs cross-system check.")
    assert out is not None
    assert "## Justification" in out
    assert "Needs cross-system check." in out


def test_compose_notes_appends_to_existing_unrelated_notes():
    out = task_cmd._compose_justification_notes(
        "Pre-existing notes line.", "Reason text.",
    )
    assert out.startswith("Pre-existing notes line.")
    assert "## Justification" in out
    assert "Reason text." in out


def test_compose_notes_refuses_when_justification_section_already_present():
    existing = "## Justification\n\nOlder reason.\n"
    with pytest.raises(click.ClickException) as exc:
        task_cmd._compose_justification_notes(existing, "Newer reason.")
    assert "already contains" in exc.value.message


def test_compose_notes_returns_none_when_no_justification():
    assert task_cmd._compose_justification_notes("anything", None) is None
    assert task_cmd._compose_justification_notes("anything", "") is None


# ---------- add_item integration ----------

def test_add_research_with_epic_in_progress_parent_no_justification(seeded_project_at_cwd):
    epic = _add_task("Epic container", status="in_progress", task_type="epic")
    new_id = task_cmd.add_item(
        "Research compare X vs Y",
        task_type="research", parent_id=epic,
    )
    notes, t = _notes_and_type(new_id)
    assert t == "research"
    assert notes in (None, "")


def test_add_research_with_epic_in_progress_parent_and_justification(seeded_project_at_cwd):
    epic = _add_task("Epic container", status="in_progress", task_type="epic")
    new_id = task_cmd.add_item(
        "Research compare X vs Y",
        task_type="research", parent_id=epic,
        justification="Needs benchmark across 3 datasets.",
    )
    notes, _ = _notes_and_type(new_id)
    assert notes is not None
    assert "## Justification" in notes
    assert "Needs benchmark across 3 datasets." in notes


def test_add_research_with_non_epic_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Plain task", status="in_progress", task_type="task")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            "Research", task_type="research", parent_id=parent,
        )
    assert "--type research requires --justification" in exc.value.message


def test_add_research_with_epic_but_not_in_progress_refused(seeded_project_at_cwd):
    epic = _add_task("Epic ready", status="ready", task_type="epic")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            "Research", task_type="research", parent_id=epic,
        )
    assert "--type research requires --justification" in exc.value.message


def test_add_research_with_no_parent_no_justification_refused(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Research", task_type="research")
    assert "--type research requires --justification" in exc.value.message


def test_add_research_with_no_parent_and_justification(seeded_project_at_cwd):
    new_id = task_cmd.add_item(
        "Research compare X vs Y", task_type="research",
        justification="Standalone investigation; no anchor epic yet.",
    )
    notes, _ = _notes_and_type(new_id)
    assert "## Justification" in notes
    assert "Standalone investigation" in notes


def test_add_non_research_no_justification_works(seeded_project_at_cwd):
    """Gate is inert for non-research adds."""
    new_id = task_cmd.add_item("Add a sample task", task_type="task")
    notes, t = _notes_and_type(new_id)
    assert t == "task"
    assert notes in (None, "")


# ---------- update_plan integration ----------

def test_update_set_research_with_epic_in_progress_parent(seeded_project_at_cwd):
    epic = _add_task("Epic", status="in_progress", task_type="epic")
    tid = _add_task("Existing task", parent_id=epic)
    task_cmd.update_plan(tid, task_type="research")
    _, t = _notes_and_type(tid)
    assert t == "research"


def test_update_set_research_with_non_epic_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Non-epic", status="in_progress", task_type="task")
    tid = _add_task("Existing task", parent_id=parent)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research")
    assert "--type research requires --justification" in exc.value.message


def test_update_set_research_with_justification_writes_notes(seeded_project_at_cwd):
    tid = _add_task("Existing task")
    task_cmd.update_plan(
        tid, task_type="research",
        justification="Cross-system comparison required.",
    )
    notes, t = _notes_and_type(tid)
    assert t == "research"
    assert notes is not None
    assert "## Justification" in notes
    assert "Cross-system comparison required." in notes


def test_update_refuses_when_justification_section_already_present(seeded_project_at_cwd):
    epic = _add_task("Epic", status="in_progress", task_type="epic")
    tid = _add_task(
        "Existing research", task_type="research", parent_id=epic,
        notes="## Justification\n\nOlder reason.\n",
    )
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(
            tid, task_type="research",
            justification="Newer reason.",
        )
    assert "already contains" in exc.value.message


def test_update_gate_does_not_fire_when_type_not_in_update(seeded_project_at_cwd):
    """If --type is not in the update, gate is inert even on a research task
    being re-parented to a non-epic. (Q1-followup: only fires when --type is set.)"""
    parent = _add_task("Non-epic", status="in_progress", task_type="task")
    epic = _add_task("Epic", status="in_progress", task_type="epic")
    tid = _add_task("Research item", task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, parent_id=parent)  # no --type, no raise
    row = db.query("SELECT parent_id FROM tasks WHERE id = ?", (tid,))
    assert row[0]["parent_id"] == parent


# ---------- E-1577: research/epic terminal gate ----------


@pytest.mark.parametrize("task_type", ["research", "epic"])
def test_confirm_refused_for_research_and_epic(seeded_project_at_cwd, task_type):
    tid = _add_task(
        "Research the cache" if task_type == "research" else "Epic container",
        task_type=task_type,
        status="in_progress",
    )
    with pytest.raises(click.ClickException) as exc:
        task_cmd.complete_item(tid)
    msg = exc.value.message.lower()
    assert task_type in msg
    assert "'confirmed'" in msg
    assert "completed" in msg  # error directs to --status completed


@pytest.mark.parametrize("task_type", ["research", "epic"])
def test_assume_refused_for_research_and_epic(seeded_project_at_cwd, task_type):
    tid = _add_task(
        "Research the cache" if task_type == "research" else "Epic container",
        task_type=task_type,
        status="in_progress",
    )
    with pytest.raises(click.ClickException) as exc:
        task_cmd.assume_item(tid)
    msg = exc.value.message.lower()
    assert task_type in msg
    assert "'assumed'" in msg
    assert "completed" in msg


@pytest.mark.parametrize("task_type", ["research", "epic"])
@pytest.mark.parametrize("status", ["confirmed", "assumed"])
def test_update_status_refused_for_research_and_epic(
    seeded_project_at_cwd, task_type, status,
):
    title = "Research the cache" if task_type == "research" else "Epic container"
    tid = _add_task(title, task_type=task_type, status="in_progress")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status=status)
    msg = exc.value.message.lower()
    assert task_type in msg
    assert f"'{status}'" in msg


@pytest.mark.parametrize("task_type", ["research", "epic"])
@pytest.mark.parametrize("status", ["confirmed", "assumed"])
def test_add_status_refused_for_research_and_epic(
    seeded_project_at_cwd, task_type, status,
):
    """Direct creation of a research/epic task in 'confirmed' or 'assumed' is refused."""
    epic = _add_task("Anchor epic", status="in_progress", task_type="epic")
    title = "Research foo" if task_type == "research" else "Build foo"
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            title,
            task_type=task_type,
            status=status,
            parent_id=epic if task_type == "research" else None,
        )
    msg = exc.value.message.lower()
    assert task_type in msg
    assert f"'{status}'" in msg


def test_update_task_type_change_to_research_blocks_assumed_status_in_same_call(
    seeded_project_at_cwd,
):
    """If --type research AND --status assumed are set in the same call,
    the gate uses the incoming type and refuses."""
    epic = _add_task("Anchor epic", status="in_progress", task_type="epic")
    tid = _add_task("Plain task", parent_id=epic)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research", status="assumed")
    assert "'assumed'" in exc.value.message.lower()


def test_update_task_to_research_then_existing_status_change(
    seeded_project_at_cwd,
):
    """A task already typed research, then setting --status confirmed in a
    separate call, also refuses (using the existing row's type)."""
    epic = _add_task("Anchor epic", status="in_progress", task_type="epic")
    tid = _add_task("Research bubble tea", task_type="research", parent_id=epic)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="confirmed")
    assert "'confirmed'" in exc.value.message.lower()


def test_research_completed_still_allowed(seeded_project_at_cwd):
    """Bug 1's gate refuses assumed/confirmed but allows completed."""
    epic = _add_task("Anchor epic", status="in_progress", task_type="epic")
    tid = _add_task("Research bubble tea", task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, status="completed", outcome="findings text")
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] == "completed"


def test_research_declined_and_obsolete_still_allowed(seeded_project_at_cwd):
    """Universal terminals (declined, obsolete) are not gated by type."""
    epic = _add_task("Anchor epic", status="in_progress", task_type="epic")
    tid = _add_task("Research X", task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, status="obsolete")
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] == "obsolete"


def test_plain_task_still_accepts_confirmed_and_assumed(seeded_project_at_cwd):
    """Bug 1 gate must not regress non-research/non-epic types."""
    tid = _add_task("Plain task")
    task_cmd.complete_item(tid)
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] == "confirmed"

    tid2 = _add_task("Other plain task")
    task_cmd.assume_item(tid2)
    row2 = db.query("SELECT status FROM tasks WHERE id = ?", (tid2,))
    assert row2[0]["status"] == "assumed"


# ---------- E-1577: cascade refuses across research/epic descendants ----------


def test_cascade_confirm_refused_when_research_descendant(seeded_project_at_cwd):
    """`task confirm --cascade` on a parent with a research descendant
    refuses loudly and names the offender."""
    parent = _add_task("Implement X", task_type="task")
    epic = _add_task("Anchor", task_type="epic", status="in_progress")
    research = _add_task(
        "Research subtree", task_type="research", parent_id=parent,
    )
    # epic just for the seeded_project (research-gate doesn't fire on _add_task)
    _ = epic
    with pytest.raises(click.ClickException) as exc:
        task_cmd.complete_item(parent, cascade=True)
    msg = exc.value.message.lower()
    assert "cascade" in msg
    assert "research" in msg
    assert f"e-{research}" in msg.lower()


def test_cascade_assume_refused_when_epic_descendant(seeded_project_at_cwd):
    parent = _add_task("Implement Y", task_type="task")
    nested_epic = _add_task("Sub-epic", task_type="epic", parent_id=parent)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.assume_item(parent, cascade=True)
    msg = exc.value.message.lower()
    assert "cascade" in msg
    assert "epic" in msg
    assert f"e-{nested_epic}" in msg.lower()


def test_cascade_confirm_passes_when_no_typed_descendants(seeded_project_at_cwd):
    parent = _add_task("Implement Z", task_type="task")
    _add_task("Sub-task", task_type="task", parent_id=parent)
    task_cmd.complete_item(parent, cascade=True)
    row = db.query("SELECT status FROM tasks WHERE id = ?", (parent,))
    assert row[0]["status"] == "confirmed"
