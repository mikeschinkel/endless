"""Tests for `endless session id` (E-1307).

The verb prints the current Endless session's integer id to stdout for
shell substitution. Failures write a diagnostic to stderr and exit
non-zero with empty stdout, so `ENDLESS_SESSION_ID="$(endless session id)"`
assigns the empty string on failure rather than a diagnostic.

These tests opt out of conftest's autouse `stub_current_session_id`
fixture (which would short-circuit the resolver to a fake `1`); they
control the resolver themselves to exercise each of its three layers
plus the failure paths.
"""

import pytest
from click.testing import CliRunner

from endless.cli import main

pytestmark = pytest.mark.no_session_stub


def _force_resolver(monkeypatch, value):
    import endless.task_cmd as task_cmd
    monkeypatch.setattr(
        task_cmd, "_current_endless_session_id", lambda: value
    )


def _force_sibling_finder(monkeypatch, value):
    import endless.task_cmd as task_cmd
    monkeypatch.setattr(
        task_cmd, "_find_sibling_claude_session", lambda: value
    )


def test_env_var_success(monkeypatch):
    """ENDLESS_SESSION_ID=42 → stdout is exactly '42\\n', exit 0."""
    monkeypatch.setenv("ENDLESS_SESSION_ID", "42")
    monkeypatch.delenv("TMUX_PANE", raising=False)

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code == 0
    assert result.stdout == "42\n"
    assert result.stderr == ""


def test_invalid_env_var(monkeypatch):
    """Non-digit env var falls through; stderr explains why, stdout empty."""
    monkeypatch.setenv("ENDLESS_SESSION_ID", "abc")
    monkeypatch.delenv("TMUX_PANE", raising=False)
    _force_resolver(monkeypatch, None)

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code != 0
    assert result.stdout == ""
    assert "not an integer" in result.stderr


def test_pane_direct_success(monkeypatch):
    """Resolver returns int (pane-direct path) → that int on stdout."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("TMUX_PANE", "%53")
    _force_resolver(monkeypatch, 7)

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code == 0
    assert result.stdout == "7\n"
    assert result.stderr == ""


def test_no_env_no_tmux(monkeypatch):
    """No env var, not in tmux → diagnostic names both missing layers."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)
    _force_resolver(monkeypatch, None)

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code != 0
    assert result.stdout == ""
    assert "not in a tmux pane" in result.stderr


def test_ambiguous_siblings(monkeypatch):
    """In tmux, multiple sibling Claude panes → 'Ambiguous' + count."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("TMUX_PANE", "%99")
    _force_resolver(monkeypatch, None)
    _force_sibling_finder(monkeypatch, (None, 3))

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code != 0
    assert result.stdout == ""
    assert "Ambiguous" in result.stderr
    assert "3 sibling" in result.stderr


def test_zero_siblings(monkeypatch):
    """In tmux but no sibling Claude pane → 'no sibling Claude pane'."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("TMUX_PANE", "%99")
    _force_resolver(monkeypatch, None)
    _force_sibling_finder(monkeypatch, (None, 0))

    result = CliRunner().invoke(main, ["session", "id"])
    assert result.exit_code != 0
    assert result.stdout == ""
    assert "no sibling Claude pane" in result.stderr
