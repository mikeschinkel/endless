"""Tests for `endless task spawn --bg` background dispatch (E-1568).

`--bg` skips the tmux window flow and instead launches `claude --bg --name
E-<id> <handoff>`, parses the dispatch short id from stdout, and records the
sessions row via the `session-query record-bg-agent` Go helper. These tests
exercise the parser and the dispatch helper with `claude` and `endless-go`
mocked, so no real background agent is launched.
"""

import subprocess

import click
import pytest

from endless import task_cmd
from endless.task_cmd import _parse_bg_short_id, _spawn_bg_dispatch


# The canonical first stdout line from `claude --bg`, plus the help block.
_CLAUDE_STDOUT = (
    "backgrounded · 7c5dcf5d · E-1568\n"
    "  claude agents             list sessions\n"
    "  claude attach 7c5dcf5d    open in this terminal\n"
)


class _FakeProc:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


# --- short-id parser -------------------------------------------------------

def test_parse_short_id_canonical():
    assert _parse_bg_short_id(_CLAUDE_STDOUT) == "7c5dcf5d"


def test_parse_short_id_leading_blank_lines_and_whitespace():
    assert _parse_bg_short_id("\n\nbackgrounded ·  abcdef01  · E-9\n") == "abcdef01"


def test_parse_short_id_alternate_length():
    assert _parse_bg_short_id("backgrounded · deadbeefcafe · E-1\n") == "deadbeefcafe"


@pytest.mark.parametrize("stdout", [
    "",
    "some unrelated output\n",
    "Backgrounded · 7c5dcf5d · E-1\n",      # wrong case — claude prints lowercase
    "backgrounded 7c5dcf5d E-1\n",          # missing the · separators
])
def test_parse_short_id_no_match(stdout):
    assert _parse_bg_short_id(stdout) is None


# --- dispatch helper -------------------------------------------------------

@pytest.fixture
def patched_dispatch(monkeypatch):
    """Stub render_handoff + the two subprocess.run shell-outs and record the
    calls. Returns the call log list."""
    calls = []

    monkeypatch.setattr(task_cmd, "render_handoff",
                        lambda *a, **k: "RENDERED-HANDOFF")
    # _branch_for_worktree shells out to git; stub so it isn't in the call log.
    monkeypatch.setattr(task_cmd, "_branch_for_worktree", lambda p: "br")
    # The soft-throttle warning (E-1572) shells out for the count; no-op it here
    # so these dispatch-mechanics tests aren't perturbed. Covered separately in
    # test_spawn_bg_throttle.py.
    monkeypatch.setattr(task_cmd, "_bg_throttle_warn", lambda item_id: None)

    def fake_run(cmd, **kw):
        calls.append((list(cmd), kw))
        if "--bg" in cmd:
            return _FakeProc(0, _CLAUDE_STDOUT, "")
        if "record-bg-agent" in cmd:
            return _FakeProc(0, "42\n", "")
        raise AssertionError(f"unexpected subprocess call: {cmd}")

    monkeypatch.setattr(subprocess, "run", fake_run)
    return calls


def test_dispatch_invokes_claude_then_records(patched_dispatch):
    _spawn_bg_dispatch(item_id=1568, title="Add --bg", cd_target="/wt",
                       task_type="task", worktree_override=False)

    claude_cmd = patched_dispatch[0][0]
    claude_kw = patched_dispatch[0][1]
    assert "--bg" in claude_cmd
    assert "--name" in claude_cmd
    assert "E-1568" in claude_cmd
    # Rendered handoff passed as positional argv.
    assert "RENDERED-HANDOFF" in claude_cmd
    # Launched with cwd = the worktree.
    assert claude_kw.get("cwd") == "/wt"

    record_cmd = patched_dispatch[1][0]
    assert "record-bg-agent" in record_cmd
    assert "--task-id" in record_cmd and "1568" in record_cmd
    assert "--short-id" in record_cmd and "7c5dcf5d" in record_cmd


def test_dispatch_worktree_override_sets_cwd(patched_dispatch):
    _spawn_bg_dispatch(item_id=1568, title="t", cd_target="/tmp",
                       task_type="task", worktree_override=True)
    assert patched_dispatch[0][1].get("cwd") == "/tmp"


def test_dispatch_parse_failure_raises_and_skips_record(monkeypatch):
    calls = []
    monkeypatch.setattr(task_cmd, "render_handoff", lambda *a, **k: "H")
    monkeypatch.setattr(task_cmd, "_branch_for_worktree", lambda p: "br")
    monkeypatch.setattr(task_cmd, "_bg_throttle_warn", lambda item_id: None)

    def fake_run(cmd, **kw):
        calls.append(list(cmd))
        if "--bg" in cmd:
            return _FakeProc(0, "no short id here\n", "")
        raise AssertionError("record-bg-agent must not be called on parse failure")

    monkeypatch.setattr(subprocess, "run", fake_run)

    with pytest.raises(click.ClickException):
        _spawn_bg_dispatch(item_id=1568, title="t", cd_target="/wt",
                           task_type="task", worktree_override=False)
    # Only the claude call happened; no row write.
    assert len(calls) == 1


def test_dispatch_claude_nonzero_exit_raises(monkeypatch):
    monkeypatch.setattr(task_cmd, "render_handoff", lambda *a, **k: "H")
    monkeypatch.setattr(task_cmd, "_branch_for_worktree", lambda p: "br")
    monkeypatch.setattr(task_cmd, "_bg_throttle_warn", lambda item_id: None)

    def fake_run(cmd, **kw):
        if "--bg" in cmd:
            return _FakeProc(1, "", "boom")
        raise AssertionError("record-bg-agent must not be called when claude fails")

    monkeypatch.setattr(subprocess, "run", fake_run)

    with pytest.raises(click.ClickException):
        _spawn_bg_dispatch(item_id=1568, title="t", cd_target="/wt",
                           task_type="task", worktree_override=False)
