"""Tests for E-1819: --analysis / --analysis-file flags on `endless task add`,
mirroring the identical pair on `endless task update` (E-1329)."""

from click.testing import CliRunner

from endless import db
from endless.cli import main


def _analysis_of(task_id: int) -> str | None:
    return db.query(
        "SELECT analysis FROM tasks WHERE id = ?", (task_id,)
    )[0]["analysis"]


def _added_id(output: str) -> int:
    # "• Added E-NNN: <title>"
    marker = "Added E-"
    idx = output.index(marker) + len(marker)
    digits = ""
    for ch in output[idx:]:
        if ch.isdigit():
            digits += ch
        else:
            break
    return int(digits)


def test_add_analysis_inline_persists(seeded_project_at_cwd):
    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Audit the X system",
        "--analysis", "multi\nline\nanalysis content",
    ])
    assert result.exit_code == 0, result.output
    tid = _added_id(result.output)
    assert _analysis_of(tid) == "multi\nline\nanalysis content"


def test_add_analysis_file_loads_content(seeded_project_at_cwd, tmp_path):
    p = tmp_path / "analysis.md"
    p.write_text("Loaded from file.\nMulti-line.")

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Audit the X system",
        "--analysis-file", str(p),
    ])
    assert result.exit_code == 0, result.output
    tid = _added_id(result.output)
    assert _analysis_of(tid) == "Loaded from file.\nMulti-line."


def test_add_analysis_and_analysis_file_together_errors(seeded_project_at_cwd, tmp_path):
    p = tmp_path / "analysis.md"
    p.write_text("Loaded from file.")

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Audit the X system",
        "--analysis", "inline",
        "--analysis-file", str(p),
    ])
    assert result.exit_code != 0
    assert "not both" in result.output
