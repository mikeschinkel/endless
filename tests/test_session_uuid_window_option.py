"""Tests for E-1585: sibling shell panes resolve the Claude session via the
@endless_session_uuid tmux window option.

A plain shell pane in the same tmux window as a Claude session has no
CLAUDECODE / CLAUDE_CODE_SESSION_ID env of its own. It discovers the sibling
Claude session by reading @endless_session_uuid (published to the window by the
Claude session's hook) and resolves/populates the row via ensure-claude-id —
this is what makes `endless --db sandbox` work from such a pane.

These tests opt out of conftest's autouse session-id stub so they exercise the
actual layered resolver.
"""

import pytest

pytestmark = pytest.mark.no_session_stub


def _as_sibling_shell(monkeypatch):
    """Env of a plain shell pane: no explicit override, no Claude env, but a
    tmux pane id. Layers 1-2 are skipped; layer 3 (pane-direct) is forced to
    miss by the caller stubbing _live_sessions to []."""
    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.delenv("CLAUDECODE", raising=False)
    monkeypatch.delenv("CLAUDE_CODE_SESSION_ID", raising=False)
    monkeypatch.setenv("TMUX_PANE", "%999")


def _no_pane_direct_match(monkeypatch):
    from endless import session_cmd
    monkeypatch.setattr(session_cmd, "_live_sessions", lambda _root: [])
    monkeypatch.setattr(session_cmd, "_project_root_for_cwd", lambda: "/tmp/proj")


def test_window_option_resolves_and_populates_sibling_session(monkeypatch):
    """@endless_session_uuid present → resolve via ensure-claude-id and return
    its int id."""
    from endless import task_cmd
    from endless.task_cmd import _current_endless_session_id

    _as_sibling_shell(monkeypatch)
    _no_pane_direct_match(monkeypatch)
    monkeypatch.setattr(task_cmd, "_tmux_window_session_uuid", lambda: "claude-uuid-1")

    calls = {}

    def _fake_ensure(uuid, process=None):
        calls["uuid"] = uuid
        calls["process"] = process
        return 314

    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _fake_ensure)

    assert _current_endless_session_id() == 314
    assert calls["uuid"] == "claude-uuid-1"


def test_window_option_passes_empty_process(monkeypatch):
    """Correctness: the sibling shell's OWN pane must NOT be recorded as the
    Claude session's process (TouchSession's collision invalidation would
    hijack it). The tier passes process=""."""
    from endless import task_cmd
    from endless.task_cmd import _current_endless_session_id

    _as_sibling_shell(monkeypatch)
    _no_pane_direct_match(monkeypatch)
    monkeypatch.setattr(task_cmd, "_tmux_window_session_uuid", lambda: "claude-uuid-1")

    captured = {}

    def _fake_ensure(uuid, process=None):
        captured["process"] = process
        return 1

    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _fake_ensure)

    _current_endless_session_id()
    assert captured["process"] == ""


def test_no_window_option_falls_through(monkeypatch):
    """No @endless_session_uuid set → tier skipped; resolver falls through to
    the sibling-DB lookup (no siblings here → None)."""
    from endless import task_cmd, session_cmd
    from endless.task_cmd import _current_endless_session_id

    _as_sibling_shell(monkeypatch)
    _no_pane_direct_match(monkeypatch)
    monkeypatch.setattr(task_cmd, "_tmux_window_session_uuid", lambda: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%999"])

    def _explode(*_a, **_kw):
        raise AssertionError("ensure-claude-id called when no window option set")

    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _explode)

    assert _current_endless_session_id() is None


def test_ensure_failure_falls_through(monkeypatch):
    """Window option present but ensure-claude-id fails (returns None) → the
    resolver continues to the sibling-DB lookup rather than stranding."""
    from endless import task_cmd, session_cmd
    from endless.task_cmd import _current_endless_session_id

    _as_sibling_shell(monkeypatch)
    _no_pane_direct_match(monkeypatch)
    monkeypatch.setattr(task_cmd, "_tmux_window_session_uuid", lambda: "claude-uuid-1")
    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", lambda _u, process=None: None)
    monkeypatch.setattr(session_cmd, "_tmux_window_pane_ids", lambda: ["%999"])

    assert _current_endless_session_id() is None


def test_claudecode_env_wins_window_option_not_consulted(monkeypatch):
    """When CLAUDECODE=1 resolves (layer 2), the window-option tier must not be
    consulted — the current process IS the Claude session."""
    from endless import task_cmd
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "uuid-self")
    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", lambda _u, process=None: 42)

    def _explode():
        raise AssertionError("window-option tier consulted despite CLAUDECODE resolve")

    monkeypatch.setattr(task_cmd, "_tmux_window_session_uuid", _explode)

    assert _current_endless_session_id() == 42
