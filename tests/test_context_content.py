"""E-1993: the `context` content name — why the task exists.

It gets its storage, heading and mirror stem from the `taskcontent` enum; the
CLI flags are declared by hand and match `analysis`: `--context` /
`--context-file` on add and update, `--clear context`, `task show --context`.
It renders by default, directly after the description.
"""

import json

from click.testing import CliRunner

from endless import content_names, db, task_cmd
from endless.cli import main


def _content(item_id: int) -> dict:
    return db.task_content(item_id)


def test_context_is_a_content_name_rendered_first():
    assert content_names.slugs()[0] == "context"
    assert content_names.label("context") == "Context"


def test_add_and_update_write_context(seeded_project_at_cwd, tmp_path):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Fix a thing", "--description", "d",
        "--context", "Today it breaks on Tuesdays.",
    ])
    assert result.exit_code == 0, result.output
    item_id = db.query("SELECT id FROM tasks WHERE title = 'Fix a thing'")[0]["id"]
    assert _content(item_id)["context"] == "Today it breaks on Tuesdays."

    f = tmp_path / "context.md"
    f.write_text("Evidence: three reports.\n")
    result = runner.invoke(main, [
        "task", "update", str(item_id), "--context-file", str(f), "--keep-status",
    ])
    assert result.exit_code == 0, result.output
    assert _content(item_id)["context"] == "Evidence: three reports.\n"


def test_context_file_refuses_an_empty_file(seeded_project_at_cwd, tmp_path):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", context="c")
    f = tmp_path / "empty.md"
    f.write_text("  \n")
    result = CliRunner().invoke(main, [
        "task", "update", str(item_id), "--context-file", str(f),
    ])
    assert result.exit_code != 0
    assert "--clear context" in result.output
    assert _content(item_id)["context"] == "c"


def test_clear_context(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", context="c")
    result = CliRunner().invoke(main, [
        "task", "update", str(item_id), "--clear", "context",
    ])
    assert result.exit_code == 0, result.output
    assert "context" not in _content(item_id)


def test_a_context_edit_never_changes_status(seeded_project_at_cwd):
    item_id = task_cmd.add_item(title="Fix a thing", description="d", plan="# p\n")
    db.execute("UPDATE tasks SET status = 'ready' WHERE id = ?", (item_id,))
    task_cmd.update_plan(item_id=item_id, context="new background")
    assert db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"] == "ready"


def test_show_renders_context_by_default_after_the_description(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Fix a thing", description="The thing is broken.",
        context="Because of the frobnicator.", analysis="Design notes here.",
    )
    out = CliRunner().invoke(main, ["task", "show", str(item_id), "--no-color"]).output
    assert "— Context —" in out
    assert "Because of the frobnicator." in out
    assert out.index("— Description —") < out.index("— Context —")
    # Other content stays gated behind its flag.
    assert "Design notes here." not in out


def test_show_agent_and_json_carry_context(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Fix a thing", description="d", context="Because reasons.",
    )
    runner = CliRunner()
    agent = runner.invoke(main, ["task", "show", str(item_id), "--agent"]).output
    assert "## Context\nBecause reasons." in agent
    data = json.loads(runner.invoke(main, ["task", "show", str(item_id), "--json"]).output)
    assert data["context"] == "Because reasons."
    assert data["context_chars"] == len("Because reasons.")


def test_show_survives_a_vocabulary_without_context(seeded_project_at_cwd, monkeypatch):
    """A worktree branched before `context` existed answers the vocabulary from
    its own older endless-go, so the item has no `context` key at all. The
    global CLI running there must still render the task."""
    item_id = task_cmd.add_item(title="Fix a thing", description="The thing is broken.")
    older = tuple(n for n in content_names.names() if n.slug != "context")
    monkeypatch.setattr(content_names, "_NAMES", older)

    result = CliRunner().invoke(main, ["task", "show", str(item_id), "--no-color"])
    assert result.exit_code == 0, result.output
    assert "The thing is broken." in result.output
    assert "— Context —" not in result.output
