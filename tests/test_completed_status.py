"""Tests for the `completed` status (E-1240).

`completed` is a findings-lane terminal, alongside `confirmed` (behavior
verified) and `assumed` (behavior believed correct, awaiting promotion). Gated
by:

  1. A TYPE rule (E-1658): only findings types — research, brainstorm, epic —
     reach `completed`; implementation types (todo, bugfix) finish via the
     verification lane and are refused it. This replaced E-1240's verb-gate,
     which put a verb check in charge of a status invariant. Enforced on BOTH the
     `task update --status` path (the Go transition table) and the `task
     complete` path (mark_completed_item's type gate, since that path bypasses
     the table).
  2. A required `--outcome` for `research`/`brainstorm` tasks, whose deliverable
     IS the outcome text (ED-1520 — keyed on TYPE). The dedicated `task complete`
     CLI command still requires `--outcome` for any type at the Click layer.
"""

import click
import pytest
from click.testing import CliRunner

from endless import db, matchers, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "underway", type_id: int = 1) -> int:
    # type_id per task_types seed (internal/schema/schema.sql):
    # 1=todo, 2=bugfix, 3=research, 4=epic, 5=brainstorm.
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _status_outcome(task_id: int) -> tuple[str, str | None]:
    row = db.query("SELECT status, outcome FROM tasks WHERE id = ?", (task_id,))
    return row[0]["status"], row[0]["outcome"]


# ─── verb_categories (E-1658, replaces is_completable_verb) ────────────────────


def test_verb_categories_audit_investigation(seeded_project_at_cwd):
    assert matchers.verb_categories("audit") == frozenset({"investigation"})


def test_verb_categories_case_insensitive(seeded_project_at_cwd):
    assert matchers.verb_categories("Audit") == frozenset({"investigation"})
    assert matchers.verb_categories("AUDIT") == frozenset({"investigation"})


def test_verb_categories_research_investigation(seeded_project_at_cwd):
    assert matchers.verb_categories("research") == frozenset({"investigation"})


def test_verb_categories_implement_action(seeded_project_at_cwd):
    assert matchers.verb_categories("implement") == frozenset({"action"})


def test_verb_categories_fix_action(seeded_project_at_cwd):
    assert matchers.verb_categories("fix") == frozenset({"action"})


def test_verb_categories_dual_verb_carries_both(seeded_project_at_cwd):
    """A genuine dual (design) carries both categories, so it is valid under an
    action type AND an investigation type."""
    assert matchers.verb_categories("design") == frozenset({"action", "investigation"})
    assert matchers.verb_categories("document") == frozenset({"action", "investigation"})


def test_verb_categories_empty_defaults_to_action(seeded_project_at_cwd):
    assert matchers.verb_categories("") == frozenset({"action"})
    assert matchers.verb_categories("   ") == frozenset({"action"})


def test_verb_categories_unknown_defaults_to_action(seeded_project_at_cwd):
    assert matchers.verb_categories("frobnicate") == frozenset({"action"})


# ─── mark_completed_item direct ───────────────────────────────────────────────


def test_completed_research_type_succeeds(seeded_project_at_cwd):
    """E-1658: `completed` is a findings-lane terminal — a research task reaches
    it. (`task complete` bypasses the Go transition table, so the type gate in
    mark_completed_item is what admits it.)"""
    tid = _add_task("Audit E-1219 for foo", type_id=_RESEARCH)
    task_cmd.mark_completed_item(tid, outcome="findings: bug in X line 42")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings: bug in X line 42"


def test_completed_brainstorm_type_succeeds(seeded_project_at_cwd):
    tid = _add_task("Explore bubble tea component patterns", type_id=_BRAINSTORM)
    task_cmd.mark_completed_item(tid, outcome="see report")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_completed_rejects_todo_type(seeded_project_at_cwd):
    """E-1658: completed-eligibility is a TYPE rule now. An implementation type
    (todo) is refused `completed` — it finishes via confirmed/assumed — even via
    the `task complete` path that bypasses the Go transition table."""
    tid = _add_task("Add new feature X", type_id=1)  # todo
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="done")
    msg = str(exc.value.message).lower()
    assert "cannot be set to status 'completed'" in msg


def test_completed_rejects_bugfix_type(seeded_project_at_cwd):
    tid = _add_task("Fix bug in parser", type_id=2)  # bugfix
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="fixed")
    assert "cannot be set to status 'completed'" in str(exc.value.message).lower()


def test_completed_requires_non_blank_outcome(seeded_project_at_cwd):
    # ED-1520: the outcome requirement is keyed on type — use a research task.
    tid = _add_task("Audit something", type_id=_RESEARCH)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_requires_non_whitespace_outcome(seeded_project_at_cwd):
    tid = _add_task("Audit something", type_id=_RESEARCH)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="   ")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_brainstorm_requires_outcome(seeded_project_at_cwd):
    """ED-1520: brainstorm is a deliverable type, so completing one without an
    outcome is refused (verb gate is exempt for brainstorm, so the outcome gate
    is what fires)."""
    tid = _add_task("Explore the pricing model", type_id=_BRAINSTORM)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_idempotent_when_already_completed(seeded_project_at_cwd):
    tid = _add_task("Audit X", status="underway", type_id=_RESEARCH)
    task_cmd.mark_completed_item(tid, outcome="first findings")
    # Second call short-circuits without erroring
    task_cmd.mark_completed_item(tid, outcome="ignored")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "first findings"  # unchanged


# ─── update_plan with status=completed ────────────────────────────────────────


def test_update_status_completed_research_succeeds(seeded_project_at_cwd):
    # Research reaches `completed` via the review track (unreviewed → completed);
    # update_plan goes through the Go transition table, so seed at `unreviewed`
    # to take the legal edge.
    tid = _add_task("Review the auth middleware", status="unreviewed", type_id=_RESEARCH)
    task_cmd.update_plan(tid, status="completed", outcome="middleware is sound; no changes needed")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "middleware is sound; no changes needed"


def test_update_status_completed_requires_outcome(seeded_project_at_cwd):
    # ED-1520: keyed on type — research task requires the outcome.
    tid = _add_task("Investigate the cache miss rate", type_id=_RESEARCH)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed")
    assert "outcome is required" in str(exc.value.message).lower()


def test_update_status_completed_rejects_todo_type(seeded_project_at_cwd):
    tid = _add_task("Implement caching layer", type_id=1)  # todo
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed", outcome="done")
    assert "cannot be set to status 'completed'" in str(exc.value.message).lower()


def test_update_status_completed_uses_new_title_if_provided(seeded_project_at_cwd):
    """If --title is also being set in the same update, the completed-gate should
    check the *new* title's lead verb, not the existing one. Uses a research task
    (which accepts investigation verbs) so both the old and new titles clear the
    E-1658 category gate; the point under test is which title the *completed*-gate
    reads. The grandfathered action-verb title is seeded via direct INSERT."""
    # Seed at 'unreviewed' — the legal predecessor of 'completed' for a research
    # task on the review track (underway→unreviewed→completed); a direct
    # underway→completed edge does not exist for research.
    tid = _add_task("Implement X", type_id=_RESEARCH, status="unreviewed")  # action verb
    # Rename to an investigation verb and complete in one shot — the new title wins.
    task_cmd.update_plan(
        tid,
        title="Audit X",  # investigation verb; also valid under research's accepts
        status="completed",
        outcome="findings here",
    )
    status, _ = _status_outcome(tid)
    assert status == "completed"


# ─── epics reach `completed` regardless of title verb (E-1658: type rule) ─────

_EPIC = 4  # task_types seed: 4 = epic
_RESEARCH = 3  # task_types seed: 3 = research
_BRAINSTORM = 5  # task_types seed: 5 = brainstorm


def test_completed_epic_via_mark(seeded_project_at_cwd):
    """An epic titled with an implementation verb still completes — completed
    eligibility is a TYPE rule (E-1658), and epic is a findings type; the title
    verb is irrelevant to status."""
    tid = _add_task("Implement the foo subsystem", type_id=_EPIC)
    task_cmd.mark_completed_item(tid, outcome="shipped via children E-a, E-b")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "shipped via children E-a, E-b"


def test_completed_epic_via_update(seeded_project_at_cwd):
    tid = _add_task("Implement the bar subsystem", type_id=_EPIC)
    task_cmd.update_plan(tid, status="completed", outcome="coordination summary")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_completed_epic_no_longer_requires_outcome(seeded_project_at_cwd):
    """ED-1520 (supersedes E-1240 for this case): epics self-complete via
    child-status derivation — there is no interactive completion step where an
    outcome could be required — so completing an epic with an empty outcome is
    allowed. The outcome requirement now keys on research/brainstorm types."""
    tid = _add_task("Implement the baz subsystem", type_id=_EPIC)
    task_cmd.mark_completed_item(tid, outcome="")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_completed_todo_refused_even_with_findings_title(seeded_project_at_cwd):
    """Regression guard: the epic/findings eligibility must not widen to
    implementation types. A todo is refused `completed` regardless of its title —
    it is a TYPE rule, not a verb one."""
    tid = _add_task("Audit the foo subsystem", type_id=1)  # todo, findings-ish title
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="done")
    assert "cannot be set to status 'completed'" in str(exc.value.message).lower()


# ─── E-1577: completed accepts existing DB outcome (merge) ────────────────────


def test_update_status_completed_satisfied_by_existing_db_outcome(seeded_project_at_cwd):
    """Bug 2: --outcome should not be required on the same call if
    tasks.outcome already holds a non-empty value. Research type so the
    requirement is actually in play (ED-1520)."""
    tid = _add_task("Audit X", type_id=_RESEARCH)
    task_cmd.update_plan(tid, outcome="findings drafted")  # standalone
    # Now flip status without re-passing --outcome. E-2016 routes research
    # through `unreviewed` on the way, and neither hop re-passes the outcome.
    task_cmd.update_plan(tid, status="unreviewed")
    task_cmd.update_plan(tid, status="completed")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings drafted"


def test_update_status_completed_new_outcome_overrides_existing(seeded_project_at_cwd):
    tid = _add_task("Audit X", type_id=_RESEARCH)
    task_cmd.update_plan(tid, outcome="old draft")
    task_cmd.update_plan(tid, status="unreviewed")
    task_cmd.update_plan(tid, status="completed", outcome="final findings")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "final findings"


def test_update_status_completed_still_refused_when_both_empty(seeded_project_at_cwd):
    """If neither existing nor new outcome is non-empty, still refused — for a
    deliverable type (ED-1520)."""
    tid = _add_task("Audit X", type_id=_RESEARCH)  # no outcome ever set
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed")
    assert "outcome is required" in str(exc.value.message).lower()


def test_update_status_unreviewed_refused_when_outcome_empty(seeded_project_at_cwd):
    """E-2016: the review gate is where the outcome now arrives, so entering it
    empty is refused. Otherwise the owner is handed nothing to read."""
    tid = _add_task("Audit X", type_id=_RESEARCH)  # no outcome ever set
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="unreviewed")
    assert "outcome is required" in str(exc.value.message).lower()


# ─── CLI ──────────────────────────────────────────────────────────────────────


def test_cli_task_complete_requires_outcome_flag(seeded_project_at_cwd):
    tid = _add_task("Audit E-1219", type_id=_RESEARCH)
    runner = CliRunner()
    result = runner.invoke(main, ["task", "complete", f"E-{tid}"])
    assert result.exit_code != 0
    assert "outcome" in result.output.lower()


def test_cli_task_complete_succeeds_for_research(seeded_project_at_cwd):
    tid = _add_task("Audit E-1219", type_id=_RESEARCH)
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "complete", f"E-{tid}",
        "--outcome", "findings: X is broken",
    ])
    assert result.exit_code == 0, result.output
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings: X is broken"


def test_cli_task_complete_rejects_todo_type(seeded_project_at_cwd):
    tid = _add_task("Add new feature", type_id=1)  # todo
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "complete", f"E-{tid}",
        "--outcome", "trying to sneak through",
    ])
    assert result.exit_code != 0
    assert "cannot be set to status 'completed'" in result.output.lower()
