"""Tests for E-1329: --type and --analysis flags on `endless task update`."""

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str, status: str = "ready", task_type: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, task_type),
    )
    return cur.lastrowid


def _type_analysis(task_id: int) -> tuple[str, str | None]:
    row = db.query(
        "SELECT type, analysis FROM tasks WHERE id = ?",
        (task_id,),
    )
    return row[0]["type"], row[0]["analysis"]


def test_update_type_changes_task_type(seeded_project_at_cwd):
    tid = _add_task("Audit the X system", task_type="task")
    task_cmd.update_plan(tid, task_type="research")
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
    tid = _add_task("Audit the X system")
    task_cmd.update_plan(
        tid, task_type="research",
        analysis="findings: X is broken",
    )
    t, a = _type_analysis(tid)
    assert t == "research"
    assert a == "findings: X is broken"


def test_update_analysis_via_at_file_loads_content(seeded_project_at_cwd, tmp_path):
    """The --analysis @file form should load file content (CLI-level test)."""
    from click.testing import CliRunner
    from endless.cli import main

    tid = _add_task("Audit the X system")
    p = tmp_path / "analysis.md"
    p.write_text("Loaded from file.\nMulti-line.")

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "update", f"E-{tid}",
        "--analysis", f"@{p}",
    ])
    assert result.exit_code == 0, result.output
    _, a = _type_analysis(tid)
    assert a == "Loaded from file.\nMulti-line."
