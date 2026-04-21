"""Tests for docs command."""

from click.testing import CliRunner

from endless.cli import main


def test_docs_shows_disabled_message(isolated_env):
    runner = CliRunner()
    result = runner.invoke(main, ["docs"])
    assert result.exit_code == 0
    assert "temporarily disabled" in result.output
