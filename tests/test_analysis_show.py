"""Tests for E-1599: render the analysis field in `endless task show`
plus the `--all-fields` flag."""

import json

from click.testing import CliRunner

from endless import db
from endless.cli import main


def _add_task(title: str, status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _set_fields(task_id: int, **fields) -> None:
    cols = ", ".join(f"{k} = ?" for k in fields)
    db.execute(
        f"UPDATE tasks SET {cols} WHERE id = ?",
        (*fields.values(), task_id),
    )


def test_analysis_flag_renders_analysis_section(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="analysis body content")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--analysis"])
    assert result.exit_code == 0, result.output
    assert "— Analysis —" in result.output
    assert "analysis body content" in result.output


def test_default_show_omits_analysis(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="hidden analysis content")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0, result.output
    assert "— Analysis —" not in result.output
    assert "hidden analysis content" not in result.output


def test_analysis_section_precedes_plan(seeded_project_at_cwd):
    """Analysis is pre-plan design content, so it renders before Text."""
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="analysis content", plan="plan content")
    runner = CliRunner()
    result = runner.invoke(
        main, ["task", "show", f"E-{tid}", "--analysis", "--plan"]
    )
    assert result.exit_code == 0, result.output
    analysis_idx = result.output.find("— Analysis —")
    plan_idx = result.output.find("— Plan —")
    assert analysis_idx != -1
    assert plan_idx != -1
    assert analysis_idx < plan_idx, "analysis section must precede plan section"


def test_all_fields_emits_every_content_section(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    _set_fields(
        tid,
        description="description blurb",
        analysis="analysis content",
        plan="plan content",
        outcome="outcome content",
    )
    child = _add_task("Implement the fix")
    _set_fields(child, parent_id=tid)
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--all-fields"])
    assert result.exit_code == 0, result.output
    assert "— Description —" in result.output
    assert "— Analysis —" in result.output
    assert "— Plan —" in result.output
    assert "— Outcome —" in result.output
    assert "— Children —" in result.output
    assert f"E-{child}" in result.output


def test_analysis_in_llm_output(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="llm-mode analysis")
    runner = CliRunner()
    result = runner.invoke(
        main, ["task", "show", f"E-{tid}", "--analysis", "--llm"]
    )
    assert result.exit_code == 0, result.output
    assert "## Analysis" in result.output
    assert "llm-mode analysis" in result.output


def test_analysis_in_json_output(seeded_project_at_cwd):
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="json analysis")
    runner = CliRunner()
    result = runner.invoke(
        main, ["task", "show", f"E-{tid}", "--analysis", "--json"]
    )
    assert result.exit_code == 0, result.output
    parsed = json.loads(result.output)
    assert parsed["analysis"] == "json analysis"


def test_json_default_returns_analysis(seeded_project_at_cwd):
    """E-2126: --json carries the analysis body with no display flag passed.

    This asserted `parsed["analysis"] is None` under E-1601, which is the defect
    E-2126 fixes: the display flags gate the human renderer, not the machine
    format."""
    tid = _add_task("Audit the X system")
    _set_fields(tid, analysis="json analysis")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--json"])
    assert result.exit_code == 0, result.output
    parsed = json.loads(result.output)
    assert parsed["analysis"] == "json analysis"
