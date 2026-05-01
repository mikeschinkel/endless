"""Tests for `endless shell-init` (E-1015)."""

from click.testing import CliRunner

from endless.cli import main


def test_shell_init_prints_helpers():
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    out = result.output
    # Both helper functions are present.
    assert "esu()" in out
    assert "escd()" in out
    # They invoke the right endless subcommands.
    assert "endless session use" in out
    assert "endless session cd" in out
    # Marker block is present so users can find/replace it later.
    assert ">>> endless shell helpers" in out
    assert "<<< endless shell helpers" in out


def test_shell_init_propagates_exit_codes():
    """The helper functions should `return $?` so a failed `endless session
    use/cd` doesn't silently succeed via `eval ""`. Verify the snippet
    contains the exit-code propagation pattern.
    """
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    # Both functions capture stdout and bail on non-zero exit.
    assert 'return $?' in result.output


def test_shell_init_idempotent():
    """Running twice produces identical output — no state, no random bits."""
    runner = CliRunner()
    a = runner.invoke(main, ["shell-init"]).output
    b = runner.invoke(main, ["shell-init"]).output
    assert a == b
