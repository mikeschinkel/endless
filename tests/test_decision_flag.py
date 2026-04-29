"""Tests for the --decision flag on task add/update (E-980).

These tests monkey-patch endless.task_cmd.add_item to skip the
endless-event Go binary (which expects a registered project the test DB
doesn't have). The stub inserts a task row directly and returns its id —
exactly what the real add_item promises post-event.
"""

import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


@pytest.fixture
def fake_add_item(monkeypatch, isolated_env):
    """Replace add_item with a direct-INSERT stub. Returns nothing — tests use db.query."""
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )

    def _stub(title, description=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False):
        task_type = task_type or "task"
        status = status or "needs_plan"
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
            "VALUES (1, ?, ?, ?, ?, ?, datetime('now'))",
            (title, description or "", status, task_type, phase),
        )
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)
    yield


def test_task_add_decision_creates_paired_decision(fake_add_item):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Implement feature X",
        "--decision", "Chose feature X over Y for better perf",
    ])
    assert result.exit_code == 0, result.output

    rows = list(db.query("SELECT id, type, status, title FROM tasks ORDER BY id"))
    assert len(rows) == 2
    assert rows[0]["type"] == "task"
    assert rows[1]["type"] == "decision"
    assert rows[1]["status"] == "confirmed"
    assert rows[1]["title"] == "Chose feature X over Y for better perf"

    deps = list(db.query(
        "SELECT source_id, target_id, dep_type FROM task_deps"
    ))
    assert len(deps) == 1
    assert deps[0]["source_id"] == rows[1]["id"]
    assert deps[0]["target_id"] == rows[0]["id"]
    assert deps[0]["dep_type"] == "relates_to"


def test_task_add_no_decision_no_extra_task(fake_add_item):
    runner = CliRunner()
    result = runner.invoke(main, ["task", "add", "Plain task"])
    assert result.exit_code == 0
    rows = list(db.query("SELECT count(*) as c FROM tasks"))
    assert rows[0]["c"] == 1
    deps = list(db.query("SELECT count(*) as c FROM task_deps"))
    assert deps[0]["c"] == 0


def test_task_update_decision_creates_decision_and_links(fake_add_item, monkeypatch):
    """task update with --decision creates a decision task and links it."""
    # Seed an existing task to update
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type, phase, created_at) "
        "VALUES (100, 1, 'Existing', 'ready', 'task', 'now', datetime('now'))"
    )
    # Stub update_plan: real one calls emit_event for status changes
    monkeypatch.setattr(task_cmd, "update_plan",
                        lambda item_id, **kwargs: db.execute(
                            "UPDATE tasks SET status = ? WHERE id = ?",
                            (kwargs.get("status") or "ready", item_id),
                        ))

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", "E-100",
        "--status", "verify",
        "--decision", "Smoke tested locally on macOS",
    ])
    assert result.exit_code == 0, result.output

    decisions = list(db.query("SELECT id, title, status FROM tasks WHERE type = 'decision'"))
    assert len(decisions) == 1
    assert decisions[0]["title"] == "Smoke tested locally on macOS"
    assert decisions[0]["status"] == "confirmed"

    deps = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert len(deps) == 1
    assert deps[0]["target_id"] == 100
    assert deps[0]["dep_type"] == "relates_to"


def test_task_update_decision_with_non_verb_text_works(fake_add_item, monkeypatch):
    """Decision text doesn't need to start with a verb (force=True applied internally)."""
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type, phase, created_at) "
        "VALUES (200, 1, 'Existing', 'ready', 'task', 'now', datetime('now'))"
    )
    monkeypatch.setattr(task_cmd, "update_plan",
                        lambda item_id, **kwargs: None)

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", "E-200",
        "--decision", "rationale that doesn't start with a verb",
    ])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT title FROM tasks WHERE type = 'decision'"))
    assert rows[0]["title"] == "rationale that doesn't start with a verb"


def test_task_update_decision_per_task_when_multiple_ids(fake_add_item, monkeypatch):
    """One decision is created per updated task ID."""
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type, phase, created_at) "
        "VALUES (300, 1, 'A', 'ready', 'task', 'now', datetime('now'))"
    )
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type, phase, created_at) "
        "VALUES (301, 1, 'B', 'ready', 'task', 'now', datetime('now'))"
    )
    monkeypatch.setattr(task_cmd, "update_plan",
                        lambda item_id, **kwargs: None)

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", "E-300", "E-301",
        "--decision", "Both ready for review",
    ])
    assert result.exit_code == 0, result.output

    decisions = list(db.query("SELECT id FROM tasks WHERE type = 'decision' ORDER BY id"))
    assert len(decisions) == 2

    deps = list(db.query(
        "SELECT source_id, target_id FROM task_deps "
        "WHERE dep_type = 'relates_to' ORDER BY target_id"
    ))
    assert len(deps) == 2
    assert deps[0]["target_id"] == 300
    assert deps[1]["target_id"] == 301
