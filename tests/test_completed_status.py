"""Tests for the `completed` status (E-1240).

`completed` is the third terminal status, alongside `confirmed` (behavior
verified) and `assumed` (behavior believed correct, awaiting promotion).
Gated by:

  1. A `completable: true` flag on the task title's lead verb in verbs.json
  2. A required `--outcome` (the outcome text IS the deliverable)
"""

import click
import pytest
from click.testing import CliRunner

from endless import db, matchers, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "in_progress", type_id: int = 1) -> int:
    # type_id per task_types seed (internal/schema/schema.sql):
    # 1=task, 2=bug, 3=research, 4=epic.
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _status_outcome(task_id: int) -> tuple[str, str | None]:
    row = db.query("SELECT status, outcome FROM tasks WHERE id = ?", (task_id,))
    return row[0]["status"], row[0]["outcome"]


# ─── is_completable_verb ──────────────────────────────────────────────────────


def test_is_completable_verb_audit_yes(seeded_project_at_cwd):
    assert matchers.is_completable_verb("audit") is True


def test_is_completable_verb_audit_case_insensitive(seeded_project_at_cwd):
    assert matchers.is_completable_verb("Audit") is True
    assert matchers.is_completable_verb("AUDIT") is True


def test_is_completable_verb_research_yes(seeded_project_at_cwd):
    assert matchers.is_completable_verb("research") is True


def test_is_completable_verb_implement_no(seeded_project_at_cwd):
    assert matchers.is_completable_verb("implement") is False


def test_is_completable_verb_fix_no(seeded_project_at_cwd):
    assert matchers.is_completable_verb("fix") is False


def test_is_completable_verb_empty_returns_false(seeded_project_at_cwd):
    assert matchers.is_completable_verb("") is False
    assert matchers.is_completable_verb("   ") is False


def test_is_completable_verb_unknown_returns_false(seeded_project_at_cwd):
    assert matchers.is_completable_verb("frobnicate") is False


# ─── mark_completed_item direct ───────────────────────────────────────────────


def test_completed_with_audit_verb_succeeds(seeded_project_at_cwd):
    tid = _add_task("Audit E-1219 for foo")
    task_cmd.mark_completed_item(tid, outcome="findings: bug in X line 42")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings: bug in X line 42"


def test_completed_with_research_verb_succeeds(seeded_project_at_cwd):
    tid = _add_task("Research bubble tea component patterns")
    task_cmd.mark_completed_item(tid, outcome="see report")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_completed_rejects_implementation_verb(seeded_project_at_cwd):
    tid = _add_task("Add new feature X")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="done")
    msg = str(exc.value.message).lower()
    assert "completable" in msg
    assert "'add'" in msg


def test_completed_rejects_fix_verb(seeded_project_at_cwd):
    tid = _add_task("Fix bug in parser")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="fixed")
    assert "completable" in str(exc.value.message).lower()


def test_completed_requires_non_blank_outcome(seeded_project_at_cwd):
    tid = _add_task("Audit something")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_requires_non_whitespace_outcome(seeded_project_at_cwd):
    tid = _add_task("Audit something")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="   ")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_idempotent_when_already_completed(seeded_project_at_cwd):
    tid = _add_task("Audit X", status="in_progress")
    task_cmd.mark_completed_item(tid, outcome="first findings")
    # Second call short-circuits without erroring
    task_cmd.mark_completed_item(tid, outcome="ignored")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "first findings"  # unchanged


# ─── update_plan with status=completed ────────────────────────────────────────


def test_update_status_completed_with_completable_verb_succeeds(seeded_project_at_cwd):
    tid = _add_task("Review the auth middleware")
    task_cmd.update_plan(tid, status="completed", outcome="middleware is sound; no changes needed")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "middleware is sound; no changes needed"


def test_update_status_completed_requires_outcome(seeded_project_at_cwd):
    tid = _add_task("Investigate the cache miss rate")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed")
    assert "outcome is required" in str(exc.value.message).lower()


def test_update_status_completed_rejects_non_completable_verb(seeded_project_at_cwd):
    tid = _add_task("Implement caching layer")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed", outcome="done")
    assert "completable" in str(exc.value.message).lower()


def test_update_status_completed_uses_new_title_if_provided(seeded_project_at_cwd):
    """If --title is also being set in the same update, the gate should
    check the *new* title's lead verb, not the existing one."""
    tid = _add_task("Implement X")  # non-completable
    # Rename and complete in one shot — the new title is what matters
    task_cmd.update_plan(
        tid,
        title="Audit X",  # now completable
        status="completed",
        outcome="findings here",
    )
    status, _ = _status_outcome(tid)
    assert status == "completed"


# ─── ED-1511 / E-1617: epics are exempt from the verb-completability gate ──────

_EPIC = 4  # task_types seed: 4 = epic


def test_completed_epic_exempt_from_verb_gate_via_mark(seeded_project_at_cwd):
    """An epic titled with an implementation verb ('implement') must still
    complete — its deliverable IS the outcome text, and the type gate already
    forces epics to terminate via 'completed'. Pre-fix this deadlocked."""
    tid = _add_task("Implement the foo subsystem", type_id=_EPIC)
    task_cmd.mark_completed_item(tid, outcome="shipped via children E-a, E-b")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "shipped via children E-a, E-b"


def test_completed_epic_exempt_from_verb_gate_via_update(seeded_project_at_cwd):
    tid = _add_task("Implement the bar subsystem", type_id=_EPIC)
    task_cmd.update_plan(tid, status="completed", outcome="coordination summary")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_completed_epic_still_requires_outcome(seeded_project_at_cwd):
    """The exemption is narrow: it drops only the verb gate. The outcome
    requirement (E-1240) still applies to epics."""
    tid = _add_task("Implement the baz subsystem", type_id=_EPIC)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="")
    assert "outcome is required" in str(exc.value.message).lower()


def test_completed_non_epic_still_gated_by_verb(seeded_project_at_cwd):
    """Regression guard: the exemption must not widen to non-epic types. A
    plain task with the same implementation-verb title is still rejected."""
    tid = _add_task("Implement the foo subsystem", type_id=1)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.mark_completed_item(tid, outcome="done")
    assert "completable" in str(exc.value.message).lower()


# ─── E-1577: completed accepts existing DB outcome (merge) ────────────────────


def test_update_status_completed_satisfied_by_existing_db_outcome(seeded_project_at_cwd):
    """Bug 2: --outcome should not be required on the same call if
    tasks.outcome already holds a non-empty value."""
    tid = _add_task("Audit X")
    task_cmd.update_plan(tid, outcome="findings drafted")  # standalone
    # Now flip status without re-passing --outcome
    task_cmd.update_plan(tid, status="completed")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings drafted"


def test_update_status_completed_new_outcome_overrides_existing(seeded_project_at_cwd):
    tid = _add_task("Audit X")
    task_cmd.update_plan(tid, outcome="old draft")
    task_cmd.update_plan(tid, status="completed", outcome="final findings")
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "final findings"


def test_update_status_completed_still_refused_when_both_empty(seeded_project_at_cwd):
    """If neither existing nor new outcome is non-empty, still refused."""
    tid = _add_task("Audit X")  # no outcome ever set
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="completed")
    assert "outcome is required" in str(exc.value.message).lower()


# ─── CLI ──────────────────────────────────────────────────────────────────────


def test_cli_task_complete_requires_outcome_flag(seeded_project_at_cwd):
    tid = _add_task("Audit E-1219")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "complete", f"E-{tid}"])
    assert result.exit_code != 0
    assert "outcome" in result.output.lower()


def test_cli_task_complete_succeeds_for_audit(seeded_project_at_cwd):
    tid = _add_task("Audit E-1219")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "complete", f"E-{tid}",
        "--outcome", "findings: X is broken",
    ])
    assert result.exit_code == 0, result.output
    status, outcome = _status_outcome(tid)
    assert status == "completed"
    assert outcome == "findings: X is broken"


def test_cli_task_complete_rejects_implementation_verb(seeded_project_at_cwd):
    tid = _add_task("Add new feature")
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "complete", f"E-{tid}",
        "--outcome", "trying to sneak through",
    ])
    assert result.exit_code != 0
    assert "completable" in result.output.lower()


# ─── lead-verb extraction edge cases ──────────────────────────────────────────


def test_lead_verb_strips_punctuation(seeded_project_at_cwd):
    """Title with trailing colon on the lead word still matches."""
    tid = _add_task("Audit: E-1219 for foo")
    task_cmd.mark_completed_item(tid, outcome="findings")
    status, _ = _status_outcome(tid)
    assert status == "completed"


def test_lead_verb_case_insensitive(seeded_project_at_cwd):
    """Capitalized title verb still matches."""
    tid = _add_task("RESEARCH the cache layer")
    task_cmd.mark_completed_item(tid, outcome="report attached")
    status, _ = _status_outcome(tid)
    assert status == "completed"
