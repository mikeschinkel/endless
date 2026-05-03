"""Tests for the ENDLESS_SANDBOX refusal gate (E-1162)."""

from click.testing import CliRunner

from endless.cli import main


def _invoke(args, env=None):
    runner = CliRunner()
    return runner.invoke(main, args, env=env or {})


def test_refuses_task_subcommand_inside_sandbox():
    """Inside ENDLESS_SANDBOX, project-bound subcommands must refuse."""
    result = _invoke(["task", "list"], env={"ENDLESS_SANDBOX": "/tmp/sb-test"})
    assert result.exit_code == 1
    assert "refusing to run 'task'" in result.output
    assert "/tmp/sb-test" in result.output
    assert "exit" in result.output


def test_refuses_top_level_subcommand_inside_sandbox():
    """A top-level subcommand (not a group) is refused too."""
    result = _invoke(["list"], env={"ENDLESS_SANDBOX": "/tmp/sb-test"})
    assert result.exit_code == 1
    assert "refusing to run 'list'" in result.output


def test_allows_shell_init_inside_sandbox():
    """shell-init is on the sandbox-safe allowlist (static stdout)."""
    result = _invoke(["shell-init"], env={"ENDLESS_SANDBOX": "/tmp/sb-test"})
    assert result.exit_code == 0
    assert "esu()" in result.output


def test_allows_help_inside_sandbox():
    """--help short-circuits before the gate fires (Click behavior)."""
    result = _invoke(["--help"], env={"ENDLESS_SANDBOX": "/tmp/sb-test"})
    assert result.exit_code == 0
    assert "Project awareness system" in result.output


def test_allows_version_inside_sandbox():
    """--version short-circuits before the gate fires."""
    result = _invoke(["--version"], env={"ENDLESS_SANDBOX": "/tmp/sb-test"})
    assert result.exit_code == 0
    assert "endless" in result.output.lower()


def test_unaffected_when_sandbox_unset():
    """Without ENDLESS_SANDBOX, the gate is invisible — normal behavior."""
    # Use a known-no-side-effect subcommand path: --help on a subcommand.
    result = _invoke(["task", "--help"], env={})
    assert result.exit_code == 0
    assert "refusing" not in result.output


def test_empty_sandbox_var_does_not_trigger_gate():
    """Empty string ENDLESS_SANDBOX (rare but possible) is treated as unset."""
    result = _invoke(["task", "--help"], env={"ENDLESS_SANDBOX": ""})
    assert result.exit_code == 0
    assert "refusing" not in result.output
