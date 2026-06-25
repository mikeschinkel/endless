"""Tests for E-1329: --type and --analysis flags on `endless task update`."""

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str, status: str = "ready", task_type: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, (SELECT id FROM task_types WHERE slug = ?), 'now', datetime('now'))",
        (title, status, task_type),
    )
    return cur.lastrowid


def _type_analysis(task_id: int) -> tuple[str, str | None]:
    row = db.query(
        "SELECT COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type, "
        "analysis FROM tasks WHERE id = ?",
        (task_id,),
    )
    return row[0]["type"], row[0]["analysis"]


def test_update_type_changes_task_type(seeded_project_at_cwd):
    # E-1544: promoting to research requires --justification (or an
    # underway epic parent). Supplying justification here.
    tid = _add_task("Audit the X system", task_type="task")
    task_cmd.update_plan(
        tid, task_type="research",
        justification="Needs deeper analysis.",
    )
    t, _ = _type_analysis(tid)
    assert t == "research"


def test_update_type_rejects_invalid(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, task_type="not-a-type")
    assert "Invalid task type" in exc.value.message


def test_update_analysis_sets_text(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    task_cmd.update_plan(tid, analysis="multi\nline\nanalysis content")
    _, a = _type_analysis(tid)
    assert a == "multi\nline\nanalysis content"


def test_update_type_and_analysis_together(seeded_project_at_cwd):
    # E-1544: promoting to research requires --justification (or an
    # underway epic parent).
    tid = _add_task("Audit the X system")
    task_cmd.update_plan(
        tid, task_type="research",
        analysis="findings: X is broken",
        justification="Needs broader system comparison.",
    )
    t, a = _type_analysis(tid)
    assert t == "research"
    assert a == "findings: X is broken"


def test_update_analysis_file_loads_content(seeded_project_at_cwd, tmp_path):
    """E-1001: --analysis-file loads file content (replaces the @file magic)."""
    from click.testing import CliRunner
    from endless.cli import main

    tid = _add_task("Audit the X system")
    p = tmp_path / "analysis.md"
    p.write_text("Loaded from file.\nMulti-line.")

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}",
        "--analysis-file", str(p),
    ])
    assert result.exit_code == 0, result.output
    _, a = _type_analysis(tid)
    assert a == "Loaded from file.\nMulti-line."


def test_update_analysis_at_path_no_longer_file_loads(seeded_project_at_cwd, tmp_path):
    """E-1001: the removed @file magic — `--analysis @path` is now stored
    literally as inline content, not loaded from the file."""
    from click.testing import CliRunner
    from endless.cli import main

    tid = _add_task("Audit the X system")
    p = tmp_path / "analysis.md"
    p.write_text("Loaded from file.")

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}",
        "--analysis", f"@{p}",
    ])
    assert result.exit_code == 0, result.output
    _, a = _type_analysis(tid)
    assert a == f"@{p}"


def test_update_text_inline_and_file_forms(seeded_project_at_cwd, tmp_path):
    """E-1001: --text stores inline content; --text-file loads from a path;
    passing both is an error."""
    from click.testing import CliRunner
    from endless.cli import main

    def _text_of(task_id: int) -> str | None:
        return db.query("SELECT text FROM tasks WHERE id = ?", (task_id,))[0]["text"]

    runner = CliRunner()

    tid = _add_task("Audit the X system")
    assert runner.invoke(main, ["task", "update", f"E-{tid}", "--text", "inline body"]).exit_code == 0
    assert _text_of(tid) == "inline body"

    p = tmp_path / "plan.md"
    p.write_text("file body\nmore")
    assert runner.invoke(main, ["task", "update", f"E-{tid}", "--text-file", str(p)]).exit_code == 0
    assert _text_of(tid) == "file body\nmore"

    both = runner.invoke(main, ["task", "update", f"E-{tid}", "--text", "x", "--text-file", str(p)])
    assert both.exit_code != 0
    assert "not both" in both.output


def test_update_outcome_file_loads_content(seeded_project_at_cwd, tmp_path):
    """E-1001: --outcome-file loads outcome content from a path."""
    from click.testing import CliRunner
    from endless.cli import main

    tid = _add_task("Audit the X system")
    p = tmp_path / "outcome.md"
    p.write_text("findings live here")

    runner = CliRunner()
    result = runner.invoke(main, ["task", "update", f"E-{tid}", "--outcome-file", str(p)])
    assert result.exit_code == 0, result.output
    row = db.query("SELECT outcome FROM tasks WHERE id = ?", (tid,))
    assert row[0]["outcome"] == "findings live here"
