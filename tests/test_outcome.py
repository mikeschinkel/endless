"""Tests for the outcome field and task decline verb (E-787)."""

import json
import subprocess
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type, phase, created_at) "
        "VALUES (1, ?, ?, 'task', 'now', datetime('now'))",
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


def test_task_replace_default_obsolete(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new)
    status, outcome = _status_outcome(old)
    assert status == "obsolete"
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
    tid = _add_task("Sample", status="in_progress")
    task_cmd.update_plan(tid, outcome="initial note")
    status, outcome = _status_outcome(tid)
    assert status == "in_progress"  # unchanged
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


def test_task_show_declined_always_shows_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="declined for testing")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "declined for testing" in result.output


def test_task_show_llm_includes_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="llm-mode reason")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--llm"])
    assert result.exit_code == 0
    assert "outcome=llm-mode reason" in result.output


def test_task_show_json_includes_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="json reason")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--json"])
    assert result.exit_code == 0
    parsed = json.loads(result.output)
    assert parsed["outcome"] == "json reason"


# ─── event log ────────────────────────────────────────────────────────────────


def test_event_log_records_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="event-log reason")
    events_dir = seeded_project_at_cwd / ".endless" / "events"
    files = list(events_dir.glob("events-*.jsonl"))
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


# ─── rebuild-db round-trip ────────────────────────────────────────────────────


def test_rebuild_db_preserves_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="round-trip reason")

    # Confirm initial state
    status, outcome = _status_outcome(tid)
    assert status == "declined"
    assert outcome == "round-trip reason"

    # Run endless-event rebuild-db against this project root.
    # Subprocess inherits XDG_CONFIG_HOME from conftest, so it resolves to the
    # same isolated DB. --confirm actually replaces the tasks table.
    binary = Path(__file__).resolve().parent.parent / "bin" / "endless-event"
    if not binary.exists():
        pytest.skip(f"endless-event binary not built at {binary}; run `just build` first")

    result = subprocess.run(
        [str(binary), "rebuild-db",
         "--project-root", str(seeded_project_at_cwd),
         "--confirm"],
        capture_output=True, text=True,
    )
    assert result.returncode == 0, f"rebuild-db failed: {result.stderr}"

    # Force a fresh DB connection so we see the rebuilt table
    db._conn = None
    status, outcome = _status_outcome(tid)
    assert status == "declined"
    assert outcome == "round-trip reason"
