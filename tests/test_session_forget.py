"""Tests for `endless session forget` and the esf shell helper (E-1159)."""

from click.testing import CliRunner

from endless.cli import main
from endless.session_cmd import SESSION_USE_EXPORTED_VARS


def test_forget_emits_unset_for_each_exported_var():
    """One 'unset <VAR>' line per documented session-use export."""
    runner = CliRunner()
    result = runner.invoke(main, ["session", "forget"])
    assert result.exit_code == 0
    out_lines = [line.strip() for line in result.output.splitlines() if line.strip()]
    expected = [f"unset {v}" for v in SESSION_USE_EXPORTED_VARS]
    assert out_lines == expected


def test_forget_only_emits_unset_lines():
    """Output is shell-evaluable: nothing but 'unset' lines (no cd, no
    export, no comments)."""
    runner = CliRunner()
    result = runner.invoke(main, ["session", "forget"])
    assert result.exit_code == 0
    for line in result.output.splitlines():
        if line.strip():
            assert line.startswith("unset "), \
                f"non-unset line in output: {line!r}"


def test_forget_takes_no_args():
    """forget is parameterless — no session-ref to resolve."""
    runner = CliRunner()
    result = runner.invoke(main, ["session", "forget", "297"])
    # Click rejects unexpected positional args
    assert result.exit_code != 0


def test_shell_init_includes_esf():
    """The shell-init snippet defines an esf function alongside esu/esp."""
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    out = result.output
    assert "esf()" in out
    # E-1164 wraps the call in _endless_run, so the literal subcommand
    # invocation is "_endless_run session forget" not "endless session forget".
    assert "session forget" in out
    # All three helpers present.
    assert "esu()" in out
    assert "esp()" in out


def test_shell_init_esf_propagates_exit_code():
    """The esf function should `return $?` on failure of the inner call,
    matching esu's pattern."""
    runner = CliRunner()
    result = runner.invoke(main, ["shell-init"])
    assert result.exit_code == 0
    out = result.output
    esf_start = out.index("esf()")
    esf_end = out.index("# <<<", esf_start)
    esf_block = out[esf_start:esf_end]
    assert "return $?" in esf_block
