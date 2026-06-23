"""Tests for `endless task continue` / `endless task pause` (E-1542).

The verbs clear the session's open revisit gate by shelling out to
`endless-go session-query gate-clear` (the direct-write Go helper — no Python
DB write, per E-1486). These tests mock that subprocess and the session
resolver, so they assert the verb wiring, the friendly no-pending message, and
that `pause` releases the active task only when a gate was actually cleared.
The DB-backed end-to-end behavior is covered by tests/tasks/e-1542-verify.sh
and the Go tests in internal/{monitor,hookcmd}.
"""

from types import SimpleNamespace
from unittest.mock import patch

import click
import pytest


def _fake_run(stdout="0", returncode=0, stderr=""):
    """Build a fake subprocess.run result for the gate-clear helper."""
    return SimpleNamespace(stdout=stdout, returncode=returncode, stderr=stderr)


def test_continue_no_session_errors():
    from endless.task_cmd import continue_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=None):
        with pytest.raises(click.ClickException) as exc:
            continue_item()
    assert "Cannot resolve current session id" in str(exc.value)


def test_continue_no_open_gate_is_friendly(capsys):
    from endless.task_cmd import continue_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(stdout="0")):
        continue_item()
    out = capsys.readouterr().out
    assert "No pending revisit prompt" in out


def test_continue_clears_gate(capsys):
    from endless.task_cmd import continue_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(stdout="1")) as run:
        continue_item()
    out = capsys.readouterr().out
    assert "continuing under the current plan" in out
    # Threads the right gate-clear invocation to the Go helper.
    args = run.call_args[0][0]
    assert "gate-clear" in args
    assert "--cleared-by" in args
    assert args[args.index("--cleared-by") + 1] == "revisit_continue"
    assert args[args.index("--session-id") + 1] == "7"
    assert args[args.index("--kind") + 1] == "revisit"


def test_pause_no_open_gate_does_not_release(capsys):
    from endless.task_cmd import pause_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(stdout="0")), \
         patch("endless.task_cmd.release_item") as release:
        pause_item()
    out = capsys.readouterr().out
    assert "No pending revisit prompt" in out
    release.assert_not_called()


def test_pause_clears_gate_and_releases(capsys):
    from endless.task_cmd import pause_item
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(stdout="1")) as run, \
         patch("endless.task_cmd.release_item") as release:
        pause_item()
    out = capsys.readouterr().out
    assert "pausing until the strategy is re-set" in out
    release.assert_called_once_with(None)
    assert args_cleared_by(run) == "revisit_pause"


def test_gate_clear_helper_raises_on_failure():
    from endless.task_cmd import _clear_revisit_gate
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(returncode=1, stderr="boom")):
        with pytest.raises(click.ClickException) as exc:
            _clear_revisit_gate("revisit_continue")
    assert "gate-clear failed" in str(exc.value)
    assert "boom" in str(exc.value)


def args_cleared_by(run_mock) -> str:
    args = run_mock.call_args[0][0]
    return args[args.index("--cleared-by") + 1]
