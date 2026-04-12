"""Tests for docs command."""

from click.testing import CliRunner

from endless import db
from endless.cli import main


def test_docs_shows_tracked_documents(isolated_env):
    project_dir = isolated_env["projects_root"] / "doc-test"
    project_dir.mkdir()
    (project_dir / "README.md").write_text("# Hello\n")
    (project_dir / "PLAN.md").write_text("# Plan\n")

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])
    runner.invoke(main, ["scan", "--project", "doc-test"])

    result = runner.invoke(main, ["docs", "doc-test"])
    assert result.exit_code == 0
    assert "README.md" in result.output
    assert "PLAN.md" in result.output
    assert "2 document(s)" in result.output


def test_docs_type_filter(isolated_env):
    project_dir = isolated_env["projects_root"] / "filter-test"
    project_dir.mkdir()
    (project_dir / "README.md").write_text("# Hello\n")
    (project_dir / "PLAN.md").write_text("# Plan\n")
    (project_dir / "notes.md").write_text("# Notes\n")

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])
    runner.invoke(main, ["scan", "--project", "filter-test"])

    result = runner.invoke(main, ["docs", "filter-test", "--type", "plan"])
    assert result.exit_code == 0
    assert "PLAN.md" in result.output
    assert "README.md" not in result.output


def test_docs_no_documents(isolated_env):
    project_dir = isolated_env["projects_root"] / "empty-test"
    project_dir.mkdir()

    runner = CliRunner()
    runner.invoke(main, ["register", str(project_dir), "--infer"])

    result = runner.invoke(main, ["docs", "empty-test"])
    assert result.exit_code == 0
    assert "No documents" in result.output
