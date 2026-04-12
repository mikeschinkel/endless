"""Tests for notes commands."""

from click.testing import CliRunner

from endless import db
from endless.cli import main


def test_add_and_list_note(isolated_env):
    project_dir = isolated_env["projects_root"] / "note-test"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])

    # Add a note
    result = runner.invoke(
        main, ["note", "add", "--project", "note-test", "Remember to update docs"]
    )
    assert result.exit_code == 0
    assert "Added note" in result.output

    # List notes
    result = runner.invoke(main, ["notes", "note-test"])
    assert result.exit_code == 0
    assert "Remember to update docs" in result.output
    assert "1 pending" in result.output


def test_resolve_note(isolated_env):
    project_dir = isolated_env["projects_root"] / "resolve-test"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])
    runner.invoke(
        main, ["note", "add", "--project", "resolve-test", "Fix this thing"]
    )

    # Get the note ID
    note_id = db.scalar(
        "SELECT id FROM notes ORDER BY id DESC LIMIT 1"
    )

    # Resolve it
    result = runner.invoke(main, ["note", "resolve", str(note_id)])
    assert result.exit_code == 0
    assert "Resolved" in result.output

    # Should not show in default notes view
    result = runner.invoke(main, ["notes", "resolve-test"])
    assert "No pending notes" in result.output

    # Should show with --all
    result = runner.invoke(
        main, ["notes", "resolve-test", "--all"]
    )
    assert "Fix this thing" in result.output


def test_notes_empty(isolated_env):
    project_dir = isolated_env["projects_root"] / "empty-notes"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])

    result = runner.invoke(main, ["notes", "empty-notes"])
    assert result.exit_code == 0
    assert "No pending notes" in result.output


def test_resolve_nonexistent_note(isolated_env):
    runner = CliRunner()
    result = runner.invoke(main, ["note", "resolve", "99999"])
    assert result.exit_code != 0
    assert "No note found" in result.output
