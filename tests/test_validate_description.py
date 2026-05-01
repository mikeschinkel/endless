"""Tests for description validation on task add / task update / decision add (E-1059).

Per E-1058 / E-1073: description is capped at 1024 chars and may not contain
embedded newlines. Validation runs at the CLI write boundary (E-962 pattern),
not via schema migration.
"""

import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


@pytest.fixture
def fake_add_item(monkeypatch, isolated_env):
    """Insert tasks directly to bypass the Go event binary in tests.

    Mirrors test_decision_flag.py's stub. Validation runs BEFORE the stub,
    so rejection paths still exercise the real validate_description.
    """
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )
    real_validate = task_cmd.validate_description

    def _stub(title, description=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False):
        task_type = task_type or "task"
        if task_type != "decision":
            task_cmd.validate_title(title, force=force)
        real_validate(description)
        status = status or ("ready" if tier == 1 else "needs_plan")
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
            "VALUES (1, ?, ?, ?, ?, ?, datetime('now'))",
            (title, description or "", status, task_type, phase),
        )
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)
    yield


# Direct unit tests on the helper

def test_validate_description_accepts_short_single_line():
    task_cmd.validate_description("a brief blurb")


def test_validate_description_accepts_none():
    task_cmd.validate_description(None)


def test_validate_description_accepts_empty():
    task_cmd.validate_description("")


def test_validate_description_accepts_exactly_1024():
    task_cmd.validate_description("a" * 1024)


def test_validate_description_rejects_1025():
    import click
    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_description("a" * 1025)
    assert "1025" in str(exc.value.message)
    assert "1024" in str(exc.value.message)


def test_validate_description_rejects_newline():
    import click
    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_description("line one\nline two")
    assert "single line" in str(exc.value.message)


def test_validate_description_rejects_carriage_return():
    import click
    with pytest.raises(click.ClickException):
        task_cmd.validate_description("line one\rline two")


# CLI integration

def test_task_add_rejects_oversized_description(fake_add_item):
    runner = CliRunner()
    long = "a" * 1025
    result = runner.invoke(main, ["task", "add", "Add a thing", "--description", long])
    assert result.exit_code != 0
    assert "1025" in result.output
    rows = list(db.query("SELECT count(*) as c FROM tasks"))
    assert rows[0]["c"] == 0


def test_task_add_rejects_newline_in_description(fake_add_item):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Add a thing", "--description", "first line\nsecond line",
    ])
    assert result.exit_code != 0
    assert "single line" in result.output
    rows = list(db.query("SELECT count(*) as c FROM tasks"))
    assert rows[0]["c"] == 0


def test_task_add_accepts_compliant_description(fake_add_item):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Add a thing", "--description", "A short blurb.",
    ])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT description FROM tasks"))
    assert rows[0]["description"] == "A short blurb."


def test_decision_add_rejects_oversized_description(fake_add_item):
    runner = CliRunner()
    long = "a" * 1025
    result = runner.invoke(main, [
        "decision", "add", "Use approach Y", "--description", long,
    ])
    assert result.exit_code != 0
    assert "1024" in result.output
    rows = list(db.query("SELECT count(*) as c FROM tasks"))
    assert rows[0]["c"] == 0


def test_decision_add_rejects_newline_in_description(fake_add_item):
    runner = CliRunner()
    result = runner.invoke(main, [
        "decision", "add", "Use approach Y",
        "--description", "Decision text.\nWith a second line.",
    ])
    assert result.exit_code != 0
    assert "single line" in result.output


def test_task_update_rejects_oversized_description(isolated_env, monkeypatch):
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
        "VALUES (1, 'Add a thing', 'short', 'needs_plan', 'task', 'now', datetime('now'))"
    )

    # Stub emit_event so update_plan doesn't try to spawn the Go event writer
    def _noop_emit(**_kwargs):
        return {"id": "E-1"}
    monkeypatch.setattr("endless.event_bridge.emit_event", _noop_emit)

    runner = CliRunner()
    long = "a" * 1025
    result = runner.invoke(main, [
        "task", "update", "1", "--description", long,
    ])
    assert result.exit_code != 0
    assert "1024" in result.output


def test_task_update_rejects_newline_in_description(isolated_env, monkeypatch):
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
        "VALUES (1, 'Add a thing', 'short', 'needs_plan', 'task', 'now', datetime('now'))"
    )

    def _noop_emit(**_kwargs):
        return {"id": "E-1"}
    monkeypatch.setattr("endless.event_bridge.emit_event", _noop_emit)

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", "1", "--description", "first\nsecond",
    ])
    assert result.exit_code != 0
    assert "single line" in result.output
