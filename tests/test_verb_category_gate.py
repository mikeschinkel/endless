"""Tests for the E-1658 creation gate: a task's title lead verb must carry a
category (`action` | `investigation`) that the task's `--type` accepts.

Model:
  - each verb carries a `category` set (matchers.verb_categories); absence
    defaults to {'action'}. Genuine duals (design, document) carry both.
  - each type declares an accepted-category set (task_cmd._TYPE_ACCEPTS):
      todo, bugfix       -> {action}
      research, brainstorm -> {investigation}
      epic               -> exempt (absent from the map; gate skipped)
  - Validity(verb, type) = type.accepts ∩ verb.category ≠ ∅.

The gate runs at BOTH task creation (`add_item`) and on `update_plan` when the
title or type is being changed — the latter closes the create-as-todo-then-flip-
to-research bypass that would otherwise strand a task in an un-terminable state.

The gate helper (`_require_verb_category_for_type`) is exercised directly here —
it raises before `add_item` reaches event emission, so no Go bridge is needed.
The CLI end-to-end acceptance path is covered by .endless/tasks/e-1658/verify.sh.
"""

import click
import pytest

from endless import db, matchers, task_cmd


def _add_valid_todo(title: str = "Fix the parser") -> int:
    """Create a valid action-verb todo directly, bypassing the creation gate, so
    the update-path tests start from a clean valid row."""
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, 'underway', 1, 'now', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


# ─── _TYPE_ACCEPTS shape ──────────────────────────────────────────────────────


def test_type_accepts_map_matches_model():
    assert task_cmd._TYPE_ACCEPTS["todo"] == frozenset({"action"})
    assert task_cmd._TYPE_ACCEPTS["bugfix"] == frozenset({"action"})
    assert task_cmd._TYPE_ACCEPTS["research"] == frozenset({"investigation"})
    assert task_cmd._TYPE_ACCEPTS["brainstorm"] == frozenset({"investigation"})
    # epic is exempt: it must NOT appear in the accepts map (gate skips it).
    assert "epic" not in task_cmd._TYPE_ACCEPTS


# ─── refusals ─────────────────────────────────────────────────────────────────


def test_action_verb_under_research_is_refused(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_verb_category_for_type("Fix the parser", "research")
    assert "fix" in str(exc.value.message).lower()


def test_investigation_verb_under_todo_is_refused(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_verb_category_for_type("Audit the ledger", "todo")
    assert "audit" in str(exc.value.message).lower()


# ─── acceptances ──────────────────────────────────────────────────────────────


def test_investigation_verb_under_research_is_accepted(seeded_project_at_cwd):
    # No raise == accepted.
    task_cmd._require_verb_category_for_type("Audit the ledger", "research")


def test_action_verb_under_todo_is_accepted(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Fix the parser", "todo")


def test_action_verb_under_bugfix_is_accepted(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Fix the parser", "bugfix")


def test_investigation_verb_under_brainstorm_is_accepted(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Explore the pricing model", "brainstorm")


# ─── dual verb: valid under both an action type and an investigation type ─────


def test_dual_verb_accepted_under_action_type(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Design the schema", "todo")


def test_dual_verb_accepted_under_investigation_type(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Design the schema", "research")


# ─── epic is exempt: any verb passes (gate skipped) ──────────────────────────


def test_epic_exempt_action_verb(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Implement the foo subsystem", "epic")


def test_epic_exempt_investigation_verb(seeded_project_at_cwd):
    task_cmd._require_verb_category_for_type("Audit the foo subsystem", "epic")


# ─── error names the verb, its category, and the type's accepted categories ──


def test_error_names_verb_category_and_accepted(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd._require_verb_category_for_type("Audit the ledger", "todo")
    msg = str(exc.value.message).lower()
    assert "audit" in msg              # the verb
    assert "investigation" in msg      # the verb's category
    assert "action" in msg             # the type's accepted category
    assert "todo" in msg               # the type


# ─── add_item wires the gate (refusal fires before event emission) ───────────


def test_add_item_refuses_investigation_verb_under_default_todo(seeded_project_at_cwd):
    """The default type is 'todo' (action-only), so an investigation-verb title
    with no explicit --type is refused at creation."""
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Audit the ledger")  # defaults to type=todo
    assert "audit" in str(exc.value.message).lower()


def test_add_item_refuses_action_verb_under_research(seeded_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        task_cmd.add_item("Fix the parser", task_type="research")
    assert "fix" in str(exc.value.message).lower()


# ─── update_plan closes the create-then-flip bypass ──────────────────────────


def test_update_flip_type_to_research_on_action_verb_refused(seeded_project_at_cwd):
    """A valid action-verb todo cannot be flipped to research — that would strand
    it (research can't reach confirmed/assumed, and 'Fix' can't reach completed)."""
    tid = _add_valid_todo("Fix the parser")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="research")
    assert "fix" in str(exc.value.message).lower()


def test_update_title_to_investigation_verb_on_todo_refused(seeded_project_at_cwd):
    """Renaming a todo's title to an investigation verb is refused — the type
    still only accepts action verbs."""
    tid = _add_valid_todo("Fix the parser")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, title="Audit the parser")
    assert "audit" in str(exc.value.message).lower()


def test_update_title_between_action_verbs_on_todo_allowed(seeded_project_at_cwd):
    """A title edit that stays within the accepted category is fine."""
    tid = _add_valid_todo("Fix the parser")
    task_cmd.update_plan(tid, title="Refactor the parser")
    row = db.query("SELECT title FROM tasks WHERE id = ?", (tid,))
    assert row[0]["title"] == "Refactor the parser"


def test_update_status_only_does_not_trip_gate(seeded_project_at_cwd):
    """A status-only update (no title/type change) must not run the category
    gate — an unrelated edit to a pre-existing row is never retroactively
    blocked."""
    tid = _add_valid_todo("Fix the parser")
    task_cmd.update_plan(tid, status="assumed")
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))
    assert row[0]["status"] == "assumed"


def test_update_flip_dual_verb_todo_to_brainstorm_allowed(seeded_project_at_cwd):
    """A dual-verb (design) todo CAN be re-typed to an investigation type —
    design carries the investigation category, so it is valid under both. Uses
    brainstorm (an investigation type with no --justification gate) to isolate
    the category check from the separate research-justification gate."""
    tid = _add_valid_todo("Design the schema")
    task_cmd.update_plan(tid, task_type="brainstorm")
    row = db.query(
        "SELECT COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS t "
        "FROM tasks WHERE id = ?",
        (tid,),
    )
    assert row[0]["t"] == "brainstorm"


# ─── E-2079: fresh-project resolver shadow (absorbed into E-1658) ─────────────


def _write_project_verbs(proj_dir, *entries: str) -> None:
    vfile = proj_dir / ".endless" / "verbs.jsonl"
    vfile.parent.mkdir(parents=True, exist_ok=True)
    vfile.write_text("".join(e if e.endswith("\n") else e + "\n" for e in entries))


def test_fresh_project_single_verb_does_not_shadow_default_categories(seeded_project_at_cwd):
    """E-2079: a fresh project whose .endless/verbs.jsonl holds ONE
    auto-registered verb must not shadow the DEFAULT_VERBS categories. Before the
    resolver fix, `_resolved_verbs()` returned the first non-empty source, so the
    one-entry project file hid all defaults and every verb resolved to 'action'."""
    _write_project_verbs(
        seeded_project_at_cwd,
        '{"value": "anchor", "definition": "to fix in place"}',
    )
    # Investigation defaults still resolve to investigation (not shadowed).
    assert matchers.verb_categories("research") == frozenset({"investigation"})
    assert matchers.verb_categories("audit") == frozenset({"investigation"})
    # The lone project verb (no category) resolves to the action default.
    assert matchers.verb_categories("anchor") == frozenset({"action"})


def test_fresh_project_research_title_accepted_under_research_type(seeded_project_at_cwd):
    """E-2079 front-door lockout: with a one-entry project verbs.jsonl, the
    creation gate must ACCEPT an investigation-led title under --type research
    (pre-fix it was refused with the false message 'research is an action verb')."""
    _write_project_verbs(
        seeded_project_at_cwd,
        '{"value": "anchor", "definition": "to fix in place"}',
    )
    # No raise == accepted.
    task_cmd._require_verb_category_for_type("Research how sessions expire", "research")


def test_field_wise_fall_through_keeps_default_category(seeded_project_at_cwd):
    """Field-wise merge: a default verb re-registered category-less into the
    project (just {value, definition}) still inherits its DEFAULT category from
    a lower layer rather than being cemented as 'action'."""
    _write_project_verbs(
        seeded_project_at_cwd,
        '{"value": "audit", "definition": "a project-specific redefinition"}',
    )
    assert matchers.verb_categories("audit") == frozenset({"investigation"})
    # The project layer's definition still wins field-wise.
    assert matchers.get_verb_definition("audit") == "a project-specific redefinition"


# ─── lead-verb extraction (punctuation / case) via the gate ──────────────────


def test_gate_lead_verb_strips_punctuation(seeded_project_at_cwd):
    """The lead verb is parsed with surrounding punctuation stripped, so a
    trailing colon on the first word still resolves the category."""
    # 'Audit:' -> 'audit' (investigation) is accepted under research.
    task_cmd._require_verb_category_for_type("Audit: the ledger", "research")
    # ...and refused under todo, proving the verb (not the punctuation) was read.
    with pytest.raises(click.ClickException):
        task_cmd._require_verb_category_for_type("Audit: the ledger", "todo")


def test_gate_lead_verb_case_insensitive(seeded_project_at_cwd):
    """A capitalized lead verb resolves the same category."""
    task_cmd._require_verb_category_for_type("RESEARCH the cache layer", "research")
    with pytest.raises(click.ClickException):
        task_cmd._require_verb_category_for_type("RESEARCH the cache layer", "todo")
