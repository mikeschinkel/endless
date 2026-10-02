"""Tests for E-1544: research-creation gate with parent-epic exemption
and --justification requirement (per ED-1504).

The gate logic lives in task_cmd._research_gate_check, exercised here at the
function level for granular coverage, plus end-to-end via task_cmd.add_item
and task_cmd.update_plan against a real isolated DB.

E-1562: the justification is its own task content row, not a `## Justification`
heading composed into notes, so a task can be re-justified and retyped to
research again.
"""

import click
import pytest

from endless import db, task_cmd


def _add_task(
    title: str,
    status: str = "ready",
    task_type: str = "todo",
    parent_id: int | None = None,
    notes: str | None = None,
) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, "
        "parent_id, created_at) "
        "VALUES (1, ?, ?, (SELECT id FROM task_types WHERE slug = ?), "
        "'now', ?, datetime('now'))",
        (title, status, task_type, parent_id),
    )
    if notes:
        # E-1531: notes are a task_content row, not a tasks column.
        db.execute(
            "INSERT INTO task_content (task_id, name, content) VALUES (?, 'notes', ?)",
            (cur.lastrowid, notes),
        )
    return cur.lastrowid


def _justification(task_id: int) -> str | None:
    return db.task_content(task_id).get("justification")


def _notes_and_type(task_id: int) -> tuple[str | None, str]:
    row = db.query(
        "SELECT COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type "
        "FROM tasks WHERE id = ?",
        (task_id,),
    )
    return db.task_content(task_id).get("notes"), row[0]["type"]


# ---------- helper-level unit tests ----------

def test_gate_passes_when_justification_provided(seeded_project_at_cwd):
    task_cmd._research_gate_check(None, "Why inline won't work.")  # no raise


def test_gate_passes_when_parent_is_epic_underway(seeded_project_at_cwd):
    epic = _add_task("Epic", status="underway", task_type="epic")
    task_cmd._research_gate_check(epic, None)  # no raise


def test_gate_refuses_when_no_parent_and_no_justification(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(None, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_epic_but_not_underway(seeded_project_at_cwd):
    epic = _add_task("Epic", status="ready", task_type="epic")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(epic, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_non_epic(seeded_project_at_cwd):
    parent = _add_task("Plain task", status="underway", task_type="todo")
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(parent, None)
    assert "--type research requires --justification" in exc.value.message


def test_gate_refuses_when_parent_not_found(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._research_gate_check(99999, None)
    assert "not found" in exc.value.message


# ---------- add_item integration ----------

def test_add_research_with_epic_underway_parent_no_justification(seeded_project_at_cwd):
    epic = _add_task("Epic container", status="underway", task_type="epic")
    new_id = task_cmd.add_item(
        "Research compare X vs Y",
        task_type="research", parent_id=epic,
    )
    notes, t = _notes_and_type(new_id)
    assert t == "research"
    assert notes in (None, "")


def test_add_research_with_epic_underway_parent_and_justification(seeded_project_at_cwd):
    epic = _add_task("Epic container", status="underway", task_type="epic")
    new_id = task_cmd.add_item(
        "Research compare X vs Y",
        task_type="research", parent_id=epic,
        justification="Needs benchmark across 3 datasets.",
    )
    notes, _ = _notes_and_type(new_id)
    assert notes in (None, "")
    assert _justification(new_id) == "Needs benchmark across 3 datasets."


def test_add_research_with_non_epic_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Plain task", status="underway", task_type="todo")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item(
            "Research", task_type="research", parent_id=parent,
        )
    assert "--type research requires --justification" in exc.value.message


def test_add_research_with_epic_but_not_underway_refused(seeded_project_at_cwd):
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
    assert notes in (None, "")
    assert _justification(new_id) == "Standalone investigation; no anchor epic yet."


def test_add_non_research_no_justification_works(seeded_project_at_cwd):
    """Gate is inert for non-research adds."""
    new_id = task_cmd.add_item("Add a sample task", task_type="todo")
    notes, t = _notes_and_type(new_id)
    assert t == "todo"
    assert notes in (None, "")


# ---------- update_plan integration ----------

def test_update_set_research_with_epic_underway_parent(seeded_project_at_cwd):
    epic = _add_task("Epic", status="underway", task_type="epic")
    # E-1658: flipping to research also requires an investigation-led title.
    tid = _add_task("Investigate the thing", parent_id=epic)
    task_cmd.update_plan(tid, task_type="research")
    _, t = _notes_and_type(tid)
    assert t == "research"


def test_update_set_research_with_non_epic_parent_refused(seeded_project_at_cwd):
    parent = _add_task("Non-epic", status="underway", task_type="todo")
    tid = _add_task("Investigate the thing", parent_id=parent)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research")
    assert "--type research requires --justification" in exc.value.message


def test_update_set_research_with_justification_writes_notes(seeded_project_at_cwd):
    tid = _add_task("Investigate the thing")
    task_cmd.update_plan(
        tid, task_type="research",
        justification="Cross-system comparison required.",
    )
    notes, t = _notes_and_type(tid)
    assert t == "research"
    assert notes in (None, "")
    assert _justification(tid) == "Cross-system comparison required."


# ---------- E-1562: justification is typed content ----------

def test_retype_research_todo_research_with_new_justification(seeded_project_at_cwd):
    """THE DEFECT E-1562 exists for: a task that had ever been research could
    never be retyped to research again, because its notes already held the
    heading and --justification refused the collision."""
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research",
        justification="First reason.",
    )
    task_cmd.update_plan(tid, task_type="todo", title="Fix the thing")
    task_cmd.update_plan(
        tid, task_type="research", title="Investigate the thing",
        justification="Second reason.",
    )
    _, t = _notes_and_type(tid)
    assert t == "research"
    assert _justification(tid) == "Second reason."


def test_justification_rewords_in_place(seeded_project_at_cwd):
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research",
        justification="Older reason.",
    )
    task_cmd.update_plan(tid, justification="Newer reason.")
    assert _justification(tid) == "Newer reason."


def test_stored_justification_satisfies_the_gate_on_retype(seeded_project_at_cwd):
    """The gate reads the row: retyping back to research needs no restatement
    when the justification is still stored."""
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research",
        justification="Still true.",
    )
    task_cmd.update_plan(tid, task_type="brainstorm")
    task_cmd.update_plan(tid, task_type="research")
    assert _justification(tid) == "Still true."


def test_cleared_justification_does_not_satisfy_the_gate(seeded_project_at_cwd):
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research",
        justification="Will be cleared.",
    )
    task_cmd.update_plan(tid, task_type="brainstorm")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research", justification="")
    assert "--type research requires --justification" in exc.value.message


def test_clear_justification_deletes_the_row(seeded_project_at_cwd):
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research",
        justification="Going away.",
    )
    task_cmd.update_plan(tid, justification="")
    assert _justification(tid) is None


def test_justification_refused_on_todo_add(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Fix the thing", task_type="todo",
                          justification="Not a research task.")
    assert "research and brainstorm" in exc.value.message


def test_justification_refused_on_todo_update(seeded_project_at_cwd):
    tid = _add_task("Fix the thing")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, justification="Not a research task.")
    assert "research and brainstorm" in exc.value.message
    assert _justification(tid) is None


def test_justification_refused_when_retyping_to_todo(seeded_project_at_cwd):
    tid = task_cmd.add_item(
        "Investigate the thing", task_type="research", justification="Why.",
    )
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="todo", title="Fix the thing",
                             justification="Still why.")
    assert "research and brainstorm" in exc.value.message


def test_justification_accepted_on_brainstorm(seeded_project_at_cwd):
    new_id = task_cmd.add_item(
        "Explore the options", task_type="brainstorm",
        justification="Needs the requester in the loop.",
    )
    assert _justification(new_id) == "Needs the requester in the loop."


def test_justification_leaves_notes_alone(seeded_project_at_cwd):
    tid = _add_task("Investigate the thing", notes="Unrelated notes.\n")
    task_cmd.update_plan(tid, task_type="research", justification="Why.")
    notes, _ = _notes_and_type(tid)
    assert notes == "Unrelated notes.\n"
    assert _justification(tid) == "Why."


def test_no_heading_composer_survives():
    assert not hasattr(task_cmd, "_compose_justification_notes")
    assert not hasattr(task_cmd, "_JUSTIFICATION_HEADING_RE")


# ---------- E-1562: moving existing `## Justification` sections ----------

def test_move_sections_moves_and_prunes(seeded_project_at_cwd):
    only = _seeded_notes("Investigate one", "## Justification\n\nOnly this.\n")
    mixed = _seeded_notes(
        "Investigate two",
        "## Justification\n\nWhy two.\n\n## From the description\n\nKept prose.\n",
    )
    plain = _seeded_notes("Investigate three", "No section here.\n")

    task_cmd.move_sections("notes", "justification")

    assert _justification(only) == "Only this."
    assert _notes_and_type(only)[0] is None
    assert _justification(mixed) == "Why two."
    assert _notes_and_type(mixed)[0] == "## From the description\n\nKept prose.\n"
    assert _justification(plain) is None
    assert _notes_and_type(plain)[0] == "No section here.\n"

    # Repeatable: nothing is left to move the second time.
    task_cmd.move_sections("notes", "justification")
    assert _justification(mixed) == "Why two."


def test_move_sections_dry_run_writes_nothing(seeded_project_at_cwd):
    tid = _seeded_notes("Investigate one", "## Justification\n\nWhy.\n")
    task_cmd.move_sections("notes", "justification", dry_run=True)
    assert _justification(tid) is None
    assert "## Justification" in _notes_and_type(tid)[0]


def _seeded_notes(title: str, notes: str) -> int:
    """A research task whose notes carry `notes`, written the way E-1544 wrote
    them: through the event pipeline, so the ledger holds the write."""
    epic = _add_task("Epic", status="underway", task_type="epic")
    tid = task_cmd.add_item(title, task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, notes=notes)
    return tid


def test_update_gate_does_not_fire_when_type_not_in_update(seeded_project_at_cwd):
    """If --type is not in the update, gate is inert even on a research task
    being re-parented to a non-epic. (Q1-followup: only fires when --type is set.)"""
    parent = _add_task("Non-epic", status="underway", task_type="todo")
    epic = _add_task("Epic", status="underway", task_type="epic")
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
        status="underway",
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
        status="underway",
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
    tid = _add_task(title, task_type=task_type, status="underway")
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
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
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
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
    tid = _add_task("Investigate the thing", parent_id=epic)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research", status="assumed")
    assert "'assumed'" in exc.value.message.lower()


def test_update_task_to_research_then_existing_status_change(
    seeded_project_at_cwd,
):
    """A task already typed research, then setting --status confirmed in a
    separate call, also refuses (using the existing row's type)."""
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
    tid = _add_task("Research bubble tea", task_type="research", parent_id=epic)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="confirmed")
    assert "'confirmed'" in exc.value.message.lower()


def test_research_completed_still_allowed(seeded_project_at_cwd):
    """Bug 1's gate refuses assumed/confirmed but allows completed.

    E-2016 put `unreviewed` in front of `completed` for research, so reaching
    the terminal is now two steps. That the terminal is still REACHABLE is what
    this test is about, and it still is.
    """
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
    tid = _add_task("Research bubble tea", task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, status="unreviewed", outcome="findings text")
    task_cmd.update_plan(tid, status="completed")
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] == "completed"


def test_research_cannot_skip_the_review_gate(seeded_project_at_cwd):
    """E-2016: a research task may not declare its own outcome finished.

    This is the defect that filed the task. E-1817's outcome went through five
    rounds of Mike's correction AFTER the session marked it completed, and
    nothing in the system had asked him first.
    """
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
    tid = _add_task("Research bubble tea", task_type="research", parent_id=epic)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed", outcome="findings text")
    assert "unreviewed" in str(exc.value)
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] != "completed"


def test_todo_is_refused_the_review_gate(seeded_project_at_cwd):
    """E-2016's inverse half: implementation work is gated by `unverified`."""
    tid = _add_task("Plain task")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="unreviewed")
    msg = str(exc.value)
    assert "unreviewed" in msg
    # The refusal must name the remedy for THIS direction, not the other one.
    assert "unverified" in msg


def test_research_declined_and_obsolete_still_allowed(seeded_project_at_cwd):
    """Universal terminals (declined, obsolete) are not gated by type."""
    epic = _add_task("Anchor epic", status="underway", task_type="epic")
    tid = _add_task("Research X", task_type="research", parent_id=epic)
    task_cmd.update_plan(tid, status="obsolete", outcome="question answered elsewhere")
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
    parent = _add_task("Implement X", task_type="todo")
    epic = _add_task("Anchor", task_type="epic", status="underway")
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
    parent = _add_task("Implement Y", task_type="todo")
    nested_epic = _add_task("Sub-epic", task_type="epic", parent_id=parent)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.assume_item(parent, cascade=True)
    msg = exc.value.message.lower()
    assert "cascade" in msg
    assert "epic" in msg
    assert f"e-{nested_epic}" in msg.lower()


def test_cascade_confirm_passes_when_no_typed_descendants(seeded_project_at_cwd):
    parent = _add_task("Implement Z", task_type="todo")
    _add_task("Sub-task", task_type="todo", parent_id=parent)
    task_cmd.complete_item(parent, cascade=True)
    row = db.query("SELECT status FROM tasks WHERE id = ?", (parent,))
    assert row[0]["status"] == "confirmed"
