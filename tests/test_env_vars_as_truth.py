"""Tests for E-1455: env vars as truth for current-pane identification.

When CLAUDECODE=1 and CLAUDE_CODE_SESSION_ID are set, the current process
IS a Claude session — no DB query needed to identify it. The resolver
short-circuits via the env vars, and lazy-creates the sessions row on the
first-event-timing race (CLI runs before any hook event has fired).

These tests opt out of conftest's autouse session-id stub (which forces
the resolver to return 1); they exercise the actual layered resolver.
"""

import pytest

pytestmark = pytest.mark.no_session_stub


def _patch_ensure(monkeypatch, value):
    """Stub _ensure_claude_session_id (which would otherwise shell out
    to endless-go session-query ensure-claude-id) to return the given
    value or None. Lets tests drive the layer without invoking Go."""
    import endless.task_cmd as task_cmd
    monkeypatch.setattr(
        task_cmd, "_ensure_claude_session_id", lambda _uuid: value
    )


def test_env_vars_short_circuit_resolves_to_lazy_created_id(monkeypatch):
    """CLAUDECODE=1 + CLAUDE_CODE_SESSION_ID → resolver returns the int id
    from the env-var helper without falling through to TMUX_PANE or
    sibling lookup. The lazy-create branch is what makes the first-event-
    timing race resolvable."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "fresh-uuid")
    monkeypatch.delenv("TMUX_PANE", raising=False)
    _patch_ensure(monkeypatch, 247)

    assert _current_endless_session_id() == 247


def test_endless_session_id_env_still_wins_over_claude_env(monkeypatch):
    """Layer 1 (ENDLESS_SESSION_ID) is the explicit caller override and
    must take precedence over the env-var layer. Tests the layer ordering
    contract: explicit > inferred."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.setenv("ENDLESS_SESSION_ID", "42")
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "uuid-X")

    # Even if the env-var helper were available, it must NOT be consulted
    # when ENDLESS_SESSION_ID is set explicitly.
    def _explode(_uuid):
        raise AssertionError("_ensure_claude_session_id called despite ENDLESS_SESSION_ID")

    import endless.task_cmd as task_cmd
    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _explode)

    assert _current_endless_session_id() == 42


def test_claudecode_unset_skips_env_var_layer(monkeypatch):
    """Without CLAUDECODE=1 the env-var layer must be skipped entirely —
    a bare shell pane that inherits CLAUDE_CODE_SESSION_ID for some odd
    reason shouldn't trigger lazy-create."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.delenv("CLAUDECODE", raising=False)
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "uuid-X")
    monkeypatch.delenv("TMUX_PANE", raising=False)

    def _explode(_uuid):
        raise AssertionError("env-var layer fired without CLAUDECODE=1")

    import endless.task_cmd as task_cmd
    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _explode)

    assert _current_endless_session_id() is None


def test_missing_session_id_uuid_skips_env_var_layer(monkeypatch):
    """CLAUDECODE=1 but no CLAUDE_CODE_SESSION_ID means we can't form an
    env-var identity; the layer must fall through cleanly."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.delenv("CLAUDE_CODE_SESSION_ID", raising=False)
    monkeypatch.delenv("TMUX_PANE", raising=False)

    def _explode(_uuid):
        raise AssertionError("env-var layer fired without CLAUDE_CODE_SESSION_ID")

    import endless.task_cmd as task_cmd
    monkeypatch.setattr(task_cmd, "_ensure_claude_session_id", _explode)

    assert _current_endless_session_id() is None


def test_env_var_helper_failure_falls_through_to_tmux_pane(
    monkeypatch, seeded_project_at_cwd, stage_live_session,
):
    """If the env-var helper returns None (Go subprocess failure, missing
    binary, etc.), the resolver continues to layer 3 (TMUX_PANE-matching
    live session). Failure must not strand the caller."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "uuid-X")
    monkeypatch.setenv("TMUX_PANE", "%55")
    _patch_ensure(monkeypatch, None)

    stage_live_session(endless_session_id=99, pane_id="%55")
    assert _current_endless_session_id() == 99


def test_env_var_layer_does_not_consult_tmux_when_resolved(monkeypatch):
    """When the env-var layer resolves, the resolver must NOT shell out to
    tmux or the live-sessions Go binary. This is the "fewer DB round-trips"
    half of the E-1455 motivation — env vars in the current process are
    fresher than any DB query of other panes."""
    from endless.task_cmd import _current_endless_session_id

    monkeypatch.delenv("ENDLESS_SESSION_ID", raising=False)
    monkeypatch.setenv("CLAUDECODE", "1")
    monkeypatch.setenv("CLAUDE_CODE_SESSION_ID", "uuid-X")
    monkeypatch.setenv("TMUX_PANE", "%77")
    _patch_ensure(monkeypatch, 555)

    from endless import session_cmd

    def _explode(*_a, **_kw):
        raise AssertionError("_live_sessions consulted after env-var layer resolved")

    monkeypatch.setattr(session_cmd, "_live_sessions", _explode)
    assert _current_endless_session_id() == 555
