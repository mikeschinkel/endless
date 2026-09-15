"""Tests for the outcome field and task decline verb (E-787)."""

import json

import click
import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _status_outcome(task_id: int) -> tuple[str, str | None]:
    row = db.query("SELECT status, outcome FROM tasks WHERE id = ?", (task_id,))
    return row[0]["status"], row[0]["outcome"]


# ─── decline ──────────────────────────────────────────────────────────────────


def test_task_decline_writes_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="wrong premise")
    status, outcome = _status_outcome(tid)
    assert status == "declined"
    assert outcome == "wrong premise"


def test_task_decline_requires_reason(seeded_project_at_cwd):
    tid = _add_task("Sample")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "decline", f"E-{tid}"])
    assert result.exit_code != 0
    assert "--reason" in result.output or "Missing option" in result.output


def test_task_decline_blank_reason_rejected(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.decline_item(tid, reason="   ")
    assert "outcome is required" in str(exc.value.message).lower()


# ─── confirm ──────────────────────────────────────────────────────────────────


def test_task_confirm_with_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.complete_item(tid, outcome="all green")
    status, outcome = _status_outcome(tid)
    assert status == "confirmed"
    assert outcome == "all green"


def test_task_confirm_without_outcome_still_works(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.complete_item(tid)
    status, outcome = _status_outcome(tid)
    assert status == "confirmed"
    assert outcome is None


# ─── assume ───────────────────────────────────────────────────────────────────


def test_task_assume_with_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.assume_item(tid, outcome="believed working")
    status, outcome = _status_outcome(tid)
    assert status == "assumed"
    assert outcome == "believed working"


# ─── replace ──────────────────────────────────────────────────────────────────


def test_task_replace_default_superseded(seeded_project_at_cwd):
    # E-2144: the unshipped default is `superseded`, not `obsolete` — the whole
    # point of this call is recording that something replaced it, and
    # `obsolete` means nothing did.
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new)
    status, outcome = _status_outcome(old)
    assert status == "superseded"
    assert outcome is None


def test_task_replace_with_status_declined_requires_outcome(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.replace_task(old, new, status="declined")
    assert "outcome is required" in str(exc.value.message).lower()


def test_task_replace_with_status_declined_and_outcome(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new, status="declined", outcome="superseded by E-NEW")
    status, outcome = _status_outcome(old)
    assert status == "declined"
    assert outcome == "superseded by E-NEW"


# ─── update ───────────────────────────────────────────────────────────────────


def test_task_update_status_declined_requires_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="declined")
    assert "outcome is required" in str(exc.value.message).lower()


def test_task_update_outcome_standalone(seeded_project_at_cwd):
    tid = _add_task("Sample", status="underway")
    task_cmd.update_plan(tid, outcome="initial note")
    status, outcome = _status_outcome(tid)
    assert status == "underway"  # unchanged
    assert outcome == "initial note"


def test_task_update_outcome_amends(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="first reason")
    task_cmd.update_plan(tid, outcome="amended reason")
    status, outcome = _status_outcome(tid)
    assert status == "declined"
    assert outcome == "amended reason"


# ─── show ─────────────────────────────────────────────────────────────────────


def test_task_show_outcome_flag_renders_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, outcome="some context")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--outcome"])
    assert result.exit_code == 0
    assert "some context" in result.output
    # E-1577: outcome renders as a "— Outcome —" section, not inline.
    assert "— Outcome —" in result.output
    # And the inline "Outcome:" label-value field is NOT used anymore.
    assert "Outcome:" not in result.output


def test_task_show_declined_hides_outcome_by_default(seeded_project_at_cwd):
    """E-1601: outcome is flag-gated for every status, declined included. With
    no flag the snapshot shows a one-line char-count placeholder, not the
    reason (replaces the old always-shows-for-declined behavior)."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="declined for testing")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Outcome:" in result.output
    assert "(--outcome to display)" in result.output
    assert "— Outcome —" not in result.output
    assert "declined for testing" not in result.output


def test_task_show_declined_outcome_flag_reveals_reason(seeded_project_at_cwd):
    """E-1601: --outcome restores the full section for a declined task."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="declined for testing")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--outcome"])
    assert result.exit_code == 0
    assert "— Outcome —" in result.output
    assert "declined for testing" in result.output
    assert "(--outcome to display)" not in result.output


def test_task_show_outcome_placeholder_char_count(seeded_project_at_cwd):
    """E-1601: the placeholder reports the exact len() of the outcome body."""
    tid = _add_task("Sample")
    body = "x" * 137
    task_cmd.update_plan(tid, outcome=body)
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    # Label column is padded, so match the count text independently of spacing.
    assert "Outcome:" in result.output
    assert "137 chars (--outcome to display)" in result.output
    assert body not in result.output


def test_task_show_completed_hides_outcome_by_default(seeded_project_at_cwd):
    """E-1601: the motivating case (E-1600) — a `completed` task's deliverable
    no longer floods the snapshot; the old status-keyed auto-display is gone."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET status = 'completed', outcome = ? WHERE id = ?",
        ("a long research deliverable body", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Outcome:" in result.output
    assert "(--outcome to display)" in result.output
    assert "— Outcome —" not in result.output
    assert "a long research deliverable body" not in result.output


def test_task_show_plan_and_analysis_placeholders(seeded_project_at_cwd):
    """E-1601: plan and analysis also collapse to flag-named placeholders."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET plan = ?, analysis = ? WHERE id = ?",
        ("body plan content", "analysis design content", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Plan:" in result.output
    assert "(--plan to display)" in result.output
    assert "Analysis:" in result.output
    assert "(--analysis to display)" in result.output
    assert "body plan content" not in result.output
    assert "analysis design content" not in result.output


def test_task_show_placeholder_precedes_description(seeded_project_at_cwd):
    """E-1601: hidden-field placeholders are single-line `Label: value` fields,
    so they render with the header group ABOVE the multi-line Description, not
    interleaved with the sections below it."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET description = ?, outcome = ? WHERE id = ?",
        ("a multi-line description body", "the outcome deliverable", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    outcome_idx = result.output.find("Outcome:")
    desc_idx = result.output.find("— Description —")
    assert outcome_idx != -1
    assert desc_idx != -1
    assert outcome_idx < desc_idx, \
        "the Outcome placeholder must render before the Description section"


def test_task_show_full_section_follows_description(seeded_project_at_cwd):
    """E-1601: the full (flagged) body still renders as a section AFTER
    Description."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET description = ?, outcome = ? WHERE id = ?",
        ("a multi-line description body", "the outcome deliverable", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--outcome"])
    assert result.exit_code == 0
    desc_idx = result.output.find("— Description —")
    outcome_idx = result.output.find("— Outcome —")
    assert desc_idx != -1
    assert outcome_idx != -1
    assert outcome_idx > desc_idx, \
        "the full Outcome section must render after the Description section"


def test_task_show_all_fields_reveals_everything(seeded_project_at_cwd):
    """E-1601: --all-fields shows every section with no placeholders left."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET plan = ?, analysis = ?, outcome = ? WHERE id = ?",
        ("body plan content", "analysis design content", "outcome deliverable", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--all-fields"])
    assert result.exit_code == 0
    assert "— Analysis —" in result.output
    assert "— Plan —" in result.output
    assert "— Outcome —" in result.output
    assert "to display)" not in result.output


def test_task_show_outcome_section_renders_after_plan(seeded_project_at_cwd):
    """E-1577: the outcome section appears AFTER the plan section."""
    tid = _add_task("Sample")
    db.execute(
        "UPDATE tasks SET plan = ?, outcome = ? WHERE id = ?",
        ("body plan content", "outcome content", tid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--plan", "--outcome"])
    assert result.exit_code == 0
    plan_idx = result.output.find("— Plan —")
    outcome_idx = result.output.find("— Outcome —")
    assert plan_idx != -1
    assert outcome_idx != -1
    assert outcome_idx > plan_idx, "outcome section must follow plan section"


def test_task_show_llm_outcome_gated(seeded_project_at_cwd):
    """E-1601: --llm collapses outcome to a char marker by default; --outcome
    pulls the body as a `## Outcome` section."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="llm-mode reason")
    runner = CliRunner()
    default = runner.invoke(main, ["task", "show", f"E-{tid}", "--llm"])
    assert default.exit_code == 0
    assert f"outcome_chars={len('llm-mode reason')}" in default.output
    assert "outcome=llm-mode reason" not in default.output
    assert "## Outcome" not in default.output
    revealed = runner.invoke(main, ["task", "show", f"E-{tid}", "--llm", "--outcome"])
    assert revealed.exit_code == 0
    assert "## Outcome" in revealed.output
    assert "llm-mode reason" in revealed.output


def test_task_show_json_outcome_ungated(seeded_project_at_cwd):
    """E-2126 inverts E-1601 for the machine format only: --json carries the
    outcome body with no flag, and outcome_chars still reports its true length.

    E-1601 applied the placeholder treatment to --json too, so this same call
    returned `"outcome": null` for a populated field. The gating was the whole
    point then; it is the defect now. What survives unchanged is the second
    half of the original assertion — outcome_chars is always present — and the
    fact that --outcome makes no difference to the payload."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="json reason")
    runner = CliRunner()
    default = json.loads(
        runner.invoke(main, ["task", "show", f"E-{tid}", "--json"]).output
    )
    assert default["outcome"] == "json reason"
    assert default["outcome_chars"] == len("json reason")
    revealed = json.loads(
        runner.invoke(main, ["task", "show", f"E-{tid}", "--json", "--outcome"]).output
    )
    assert revealed == default, "a display flag must not change the JSON payload"


# ─── event log ────────────────────────────────────────────────────────────────


def test_event_log_records_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="event-log reason")
    events_dir = seeded_project_at_cwd / ".endless" / "db-ledger"
    files = list(events_dir.glob("db-entries-*.jsonl"))
    assert files, "no event log file written"
    found = False
    for f in files:
        for line in f.read_text().splitlines():
            evt = json.loads(line)
            if (evt.get("kind") == "task.status_changed"
                    and evt.get("entity", {}).get("id") == str(tid)):
                payload = evt["payload"]
                if (payload.get("new_status") == "declined"
                        and payload.get("outcome") == "event-log reason"):
                    found = True
                    break
    assert found, "decline event with outcome not found in event log"


# ─── ledger round-trip ────────────────────────────────────────────────────────
#
# test_rebuild_db_preserves_outcome lived here: it declined a task, ran
# `endless-go event rebuild-db --confirm`, and read the outcome back out of the
# real database. E-2062 refuses that flag deliberately — its copy-back destroys
# four tables it cannot restore — and the claim never needed it. The two halves
# it was making are covered where each one lives:
#
#   - the decline EMITS the outcome:
#     test_event_log_records_outcome, immediately above.
#   - replaying that event REPRODUCES the outcome:
#     internal/events/projector_test.go,
#     TestProjectToTempDB_StatusChangeCarriesOutcome.
#
# That pair asserts the round trip with no built binary, no database write, and
# no dependence on a command that is disabled.
