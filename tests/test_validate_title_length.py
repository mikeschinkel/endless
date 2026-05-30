"""Tests for the 100-character title cap (E-1517).

Length is a structural constraint enforced in validate_title before the verb
check, so `force=True` does NOT bypass it. Runs on the CLI write boundary
(task add / task update --title), matching the description-length pattern.
"""

import pytest
import click
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


# Direct unit tests on validate_title's length branch.

def test_validate_title_accepts_100_chars():
    title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add "))
    assert len(title) == task_cmd.TITLE_MAX_LENGTH
    task_cmd.validate_title(title)


def test_validate_title_rejects_101_chars():
    title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    assert len(title) == task_cmd.TITLE_MAX_LENGTH + 1
    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title(title)
    msg = exc.value.message
    assert str(task_cmd.TITLE_MAX_LENGTH) in msg
    assert str(len(title)) in msg


def test_validate_title_force_does_not_bypass_length():
    title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    with pytest.raises(click.ClickException):
        task_cmd.validate_title(title, force=True)


def test_validate_title_length_message_names_shape():
    long = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    with pytest.raises(click.ClickException) as exc:
        task_cmd.validate_title(long)
    msg = exc.value.message
    # Sanity-check that the guidance is actually surfaced — not just the count.
    assert "Shape:" in msg
    assert "<verb>" in msg
    assert "Symptom" in msg


# CLI integration: matches tests/test_validate_description.py shape.

@pytest.fixture
def fake_add_item(monkeypatch, isolated_env):
    """Insert tasks directly to bypass the Go event binary in tests.

    Validation runs BEFORE the stub, so rejection paths still exercise the
    real validate_title.
    """
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )

    def _stub(title, description=None, text_file=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False, **kwargs):
        task_type = task_type or "task"
        if task_type != "decision":
            task_cmd.validate_title(title, force=force)
        task_cmd.validate_description(description)
        status = status or ("ready" if tier == 1 else "needs_plan")
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
            "VALUES (1, ?, ?, ?, ?, ?, datetime('now'))",
            (title, description or "", status, task_type, phase),
        )
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)
    yield


def test_task_add_rejects_oversized_title(fake_add_item):
    runner = CliRunner()
    long_title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    result = runner.invoke(main, ["task", "add", long_title])
    assert result.exit_code != 0
    assert str(len(long_title)) in result.output
    assert str(task_cmd.TITLE_MAX_LENGTH) in result.output
    rows = list(db.query("SELECT count(*) as c FROM tasks"))
    assert rows[0]["c"] == 0


def test_task_add_accepts_exactly_100_char_title(fake_add_item):
    runner = CliRunner()
    title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add "))
    assert len(title) == task_cmd.TITLE_MAX_LENGTH
    result = runner.invoke(main, ["task", "add", title])
    assert result.exit_code == 0, result.output
    rows = list(db.query("SELECT title FROM tasks"))
    assert rows[0]["title"] == title


def test_task_add_force_does_not_bypass_length(fake_add_item):
    runner = CliRunner()
    long_title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    result = runner.invoke(main, ["task", "add", long_title, "--force"])
    assert result.exit_code != 0
    assert str(task_cmd.TITLE_MAX_LENGTH) in result.output


def test_task_update_rejects_oversized_title(isolated_env, monkeypatch):
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
    long_title = "Add " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Add ") + 1)
    result = runner.invoke(main, ["task", "update", "1", "--title", long_title])
    assert result.exit_code != 0
    assert str(task_cmd.TITLE_MAX_LENGTH) in result.output


def test_decision_add_skips_title_length_check(fake_add_item):
    """Decisions bypass validate_title entirely (E-1517 scope: tasks only)."""
    runner = CliRunner()
    long_title = "Use " + "x" * (task_cmd.TITLE_MAX_LENGTH - len("Use ") + 1)
    result = runner.invoke(main, ["decision", "add", long_title])
    # decisions skip validate_title, so a >100-char decision title is accepted.
    assert result.exit_code == 0, result.output
