"""Tests for the Claude hook sync/async contract (E-1901).

`Stop` had to become synchronous for the verbatim-relay gate to work at all: an
async hook runs in the background and the stop proceeds immediately, so
`decision:"block"` is never read. This is worth pinning precisely because the
failure is invisible — the hook still fires, the DB row is still written, no
error appears anywhere, and the gate simply never gates.
"""

from endless import setup


def _entry(command="/usr/local/bin/endless-go hook claude", is_async=True):
    return {"hooks": [{"type": "command", "command": command, "async": is_async}]}


def test_stop_is_synchronous():
    assert "Stop" in setup.SYNC_EVENTS, (
        "an async Stop hook cannot return decision:'block', silently disabling "
        "the E-1901 relay gate"
    )


def test_session_end_stays_async():
    """Only events whose response Claude reads need to be synchronous; SessionEnd
    is fire-and-forget cleanup and should not block session teardown."""
    assert "SessionEnd" not in setup.SYNC_EVENTS


def test_repair_flips_a_stale_async_stop():
    """The path that matters for existing installs: `setup_claude_hook` returns
    early once a hook is present, so without this repair every machine already
    running Endless would keep a non-blocking Stop hook forever."""
    settings = {"hooks": {"Stop": [_entry(is_async=True)]}}
    repaired = setup._repair_hook_async_flags(settings)
    assert repaired == ["Stop"]
    assert settings["hooks"]["Stop"][0]["hooks"][0]["async"] is False


def test_repair_is_idempotent():
    settings = {"hooks": {"Stop": [_entry(is_async=False)]}}
    assert setup._repair_hook_async_flags(settings) == []
    assert settings["hooks"]["Stop"][0]["hooks"][0]["async"] is False


def test_repair_fixes_a_missing_async_key():
    """A hand-written entry may omit `async` entirely; Claude Code's default is
    not something to bet the gate on, so an absent flag is still a mismatch."""
    settings = {"hooks": {"Stop": [{"hooks": [
        {"type": "command", "command": "/usr/local/bin/endless-go hook claude"}
    ]}]}}
    assert setup._repair_hook_async_flags(settings) == ["Stop"]
    assert settings["hooks"]["Stop"][0]["hooks"][0]["async"] is False


def test_repair_ignores_foreign_hooks():
    """Another tool's hook in the same event is none of our business."""
    settings = {"hooks": {"Stop": [
        {"hooks": [{"type": "command", "command": "/usr/bin/other-tool", "async": True}]},
    ]}}
    assert setup._repair_hook_async_flags(settings) == []
    assert settings["hooks"]["Stop"][0]["hooks"][0]["async"] is True


def test_repair_restores_async_on_a_fire_and_forget_event():
    """The repair enforces agreement with SYNC_EVENTS in BOTH directions, so a
    hand-edited sync SessionEnd is corrected too."""
    settings = {"hooks": {"SessionEnd": [_entry(is_async=False)]}}
    assert setup._repair_hook_async_flags(settings) == ["SessionEnd"]
    assert settings["hooks"]["SessionEnd"][0]["hooks"][0]["async"] is True


def test_repair_handles_empty_settings():
    assert setup._repair_hook_async_flags({}) == []
