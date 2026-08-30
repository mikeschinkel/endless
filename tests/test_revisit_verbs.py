"""Tests for `endless task continue` (E-1542, as amended by E-1968).

The verb clears the session's open revisit gate by shelling out to
`endless-go session-query gate-clear` (the direct-write Go helper — no Python
DB write, per E-1486). These tests mock that subprocess and the session
resolver, so they assert the verb wiring and the friendly no-pending message.
E-1968 removed its counterpart `task pause`; the last test pins that removal.
The DB-backed end-to-end behavior is covered by .endless/tasks/e-1542/verify.sh
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


def test_pause_verb_is_gone():
    """E-1968 removed `task pause`. Pausing on the epic-revisit gate is
    declining to clear it — the gate keeps blocking, and auto-clears when the
    epic leaves `revisit`. The verb only existed to carry a release of the
    session's task, which ED-1560's write-once `task_id` forbids.
    """
    import endless.task_cmd as task_cmd
    from endless.cli import task_cmd as task_group

    assert not hasattr(task_cmd, "pause_item")
    assert "pause" not in task_group.commands
    assert "continue" in task_group.commands


def test_gate_clear_helper_raises_on_failure():
    from endless.task_cmd import _clear_revisit_gate
    with patch("endless.task_cmd._current_endless_session_id", return_value=7), \
         patch("subprocess.run", return_value=_fake_run(returncode=1, stderr="boom")):
        with pytest.raises(click.ClickException) as exc:
            _clear_revisit_gate("revisit_continue")
    assert "gate-clear failed" in str(exc.value)
    assert "boom" in str(exc.value)

