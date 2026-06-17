"""Tests for `endless setup shell-helpers` / `remove-shell-helpers` (E-1592)."""

from pathlib import Path

from click.testing import CliRunner

from endless import setup
from endless.cli import main
from endless.setup import (
    SHELL_HELPERS_COMMENT,
    SHELL_HELPERS_EVAL,
)


def _make_rcfile(tmp_path: Path, body: str = "# existing rc\n") -> Path:
    rc = tmp_path / "zshrc"
    rc.write_text(body)
    return rc


def _install(rc: Path) -> str:
    """Run install against `rc`, answering the path prompt + Proceed."""
    runner = CliRunner()
    # Prompts: "Is <default> where ...?" (n), "Path", "Proceed?" (y).
    result = runner.invoke(
        main,
        ["setup", "shell-helpers"],
        input=f"n\n{rc}\ny\n",
    )
    assert result.exit_code == 0, result.output
    return result.output


def _remove(rc: Path) -> str:
    runner = CliRunner()
    result = runner.invoke(
        main,
        ["setup", "remove-shell-helpers"],
        input=f"{rc}\n",
    )
    assert result.exit_code == 0, result.output
    return result.output


def test_install_appends_block(tmp_path):
    rc = _make_rcfile(tmp_path)
    _install(rc)
    content = rc.read_text()
    assert SHELL_HELPERS_COMMENT in content
    assert SHELL_HELPERS_EVAL in content
    # Original content preserved.
    assert "# existing rc" in content


def test_install_idempotent(tmp_path):
    rc = _make_rcfile(tmp_path)
    _install(rc)
    after_first = rc.read_text()

    out = _install(rc)
    after_second = rc.read_text()
    # No duplicate append; file unchanged on the second run.
    assert after_first == after_second
    assert after_second.count(SHELL_HELPERS_EVAL) == 1
    assert "already installed" in out


def test_install_detects_manual_eval_line(tmp_path):
    """A bare eval line (no marker comment) still counts as installed."""
    rc = _make_rcfile(tmp_path, f"# existing rc\n{SHELL_HELPERS_EVAL}\n")
    out = _install(rc)
    assert "already installed" in out
    assert rc.read_text().count(SHELL_HELPERS_EVAL) == 1


def test_remove_strips_block(tmp_path):
    rc = _make_rcfile(tmp_path)
    _install(rc)
    _remove(rc)
    content = rc.read_text()
    assert SHELL_HELPERS_COMMENT not in content
    assert SHELL_HELPERS_EVAL not in content
    # Unrelated content survives removal.
    assert "# existing rc" in content


def test_remove_when_absent_is_noop(tmp_path):
    rc = _make_rcfile(tmp_path)
    out = _remove(rc)
    assert "No Endless session shell helpers found" in out
    assert rc.read_text() == "# existing rc\n"


def test_install_proceed_declined_writes_nothing(tmp_path):
    rc = _make_rcfile(tmp_path)
    runner = CliRunner()
    # Decline the final Proceed prompt.
    result = runner.invoke(
        main,
        ["setup", "shell-helpers"],
        input=f"n\n{rc}\nn\n",
    )
    assert result.exit_code == 0, result.output
    assert SHELL_HELPERS_EVAL not in rc.read_text()
    # Manual instructions are shown instead.
    assert "install manually" in result.output


def test_helpers_block_constant_shape():
    """The block is the marker comment followed by the eval line."""
    assert setup.SHELL_HELPERS_BLOCK == (
        f"{SHELL_HELPERS_COMMENT}\n{SHELL_HELPERS_EVAL}"
    )
    # Fail-loud install: bare eval, no `command -v` PATH guard (E-1592).
    assert "command -v" not in setup.SHELL_HELPERS_BLOCK
    assert SHELL_HELPERS_EVAL == 'eval "$(endless shell-init)"'
