"""E-1562: the `justification` content name — why a research or brainstorm task
cannot be inline in a do-task.

It was a `## Justification` heading composed into notes (E-1544). As its own
content it gets storage, heading and mirror stem from the `taskcontent` enum,
renders first — before Context — and is written, reworded and cleared like any
other content.
"""

from click.testing import CliRunner

from endless import content_names, db, task_cmd
from endless.cli import main


def _justification(item_id: int) -> str | None:
    return db.task_content(item_id).get("justification")


def test_justification_is_declared_first():
    assert content_names.slugs()[0] == "justification"
    assert content_names.label("justification") == "Justification"


def test_show_renders_justification_before_context(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Investigate a thing", description="The thing is unclear.",
        task_type="research", justification="Spans three systems.",
        context="Because of the frobnicator.",
    )
    runner = CliRunner()
    out = runner.invoke(main, ["task", "show", str(item_id), "--no-color", "--all-fields"]).output
    assert out.index("— Description —") < out.index("— Justification —") < out.index("— Context —")
    assert "Spans three systems." in out

    out = runner.invoke(main, ["task", "show", str(item_id), "--no-color", "--justification"]).output
    assert "Spans three systems." in out
    assert "Because of the frobnicator." not in out, "other content stays behind its own flag"


def test_show_hides_justification_by_default(seeded_project_at_cwd):
    item_id = task_cmd.add_item(
        title="Investigate a thing", description="d",
        task_type="research", justification="Spans three systems.",
    )
    out = CliRunner().invoke(main, ["task", "show", str(item_id), "--no-color"]).output
    assert "Spans three systems." not in out
    assert "(--justification to display)" in out


def test_cli_add_update_and_clear(seeded_project_at_cwd):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Investigate a thing", "--description", "d",
        "--type", "research", "--justification", "First.",
    ])
    assert result.exit_code == 0, result.output
    item_id = db.query("SELECT id FROM tasks WHERE title = 'Investigate a thing'")[0]["id"]
    assert _justification(item_id) == "First."

    result = runner.invoke(main, [
        "task", "update", str(item_id), "--justification", "Second.",
    ])
    assert result.exit_code == 0, result.output
    assert _justification(item_id) == "Second."

    result = runner.invoke(main, [
        "task", "update", str(item_id), "--clear", "justification",
    ])
    assert result.exit_code == 0, result.output
    assert _justification(item_id) is None


def test_cli_refuses_justification_on_a_todo(seeded_project_at_cwd):
    result = CliRunner().invoke(main, [
        "task", "add", "Fix a thing", "--description", "d",
        "--justification", "Not research.",
    ])
    assert result.exit_code != 0
    assert "research and brainstorm" in result.output


def test_epic_update_does_not_offer_clear_justification():
    result = CliRunner().invoke(main, ["epic", "update", "--help"])
    assert result.exit_code == 0
    assert "justification" not in result.output
