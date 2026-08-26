"""Tests for adding hook events an existing install never got (E-2073).

`_repair_hook_async_flags` fixes the flag on an entry that already exists. It
cannot help an event that was never installed, and `_has_endless_hook` returns
True on finding endless-go under ANY event — so `setup_claude_hook` early-returns
and the missing event stays missing while the command reports itself set up.

Found live: the machine developing Endless had no PreToolUse entry at all, which
made every blocking gate Endless has inert — the worktree-removal refusal, the
commit-on-main refusal, the cwd gate, the revisit gate, the sqlite refusal. The
failure is invisible in exactly the way E-1901's was: hooks fire, rows are
written, nothing errors, and the gates simply never gate.
"""

from endless import setup

HOOK_BIN = "/usr/local/bin/endless-go"
ENDLESS_CMD = HOOK_BIN + " hook claude"


def _entry(command=ENDLESS_CMD, is_async=True):
    return {"hooks": [{"type": "command", "command": command, "async": is_async}]}


def _commands(settings, event):
    return [
        h.get("command", "")
        for entry in settings["hooks"].get(event, [])
        for h in entry.get("hooks", [])
    ]


def test_the_live_defect_a_missing_pretooluse_is_added():
    """The exact shape found on the development machine: PostToolUse installed,
    PreToolUse holding only foreign hooks, so `_has_endless_hook` says yes."""
    settings = {
        "hooks": {
            "PreToolUse": [_entry(command="/usr/local/bin/claude-log-hook")],
            "PostToolUse": [_entry(is_async=False)],
        }
    }
    assert setup._has_endless_hook(settings), "precondition: setup would early-return"

    added = setup._repair_missing_hook_events(settings, HOOK_BIN)

    assert "PreToolUse" in added
    assert ENDLESS_CMD in _commands(settings, "PreToolUse")


def test_a_foreign_hook_on_the_same_event_is_preserved():
    """Other tools register hooks too. Adding ours must not evict theirs, and
    theirs must keep running first — we append, never prepend."""
    settings = {"hooks": {"PreToolUse": [_entry(command="/opt/other-tool")]}}

    setup._repair_missing_hook_events(settings, HOOK_BIN)

    cmds = _commands(settings, "PreToolUse")
    assert cmds == ["/opt/other-tool", ENDLESS_CMD]


def test_added_events_get_the_right_async_flag():
    """A hook added async cannot block, which would reproduce E-1901's silent
    failure on every event this repair touches."""
    settings = {"hooks": {"PostToolUse": [_entry(is_async=False)]}}

    setup._repair_missing_hook_events(settings, HOOK_BIN)

    for event in setup.CLAUDE_HOOK_EVENTS:
        want_async = event not in setup.SYNC_EVENTS
        entry = [
            h
            for entry in settings["hooks"][event]
            for h in entry["hooks"]
            if "endless-go" in h["command"]
        ][0]
        assert entry["async"] is want_async, event


def test_every_hooked_event_is_present_afterwards():
    settings = {"hooks": {}}

    added = setup._repair_missing_hook_events(settings, HOOK_BIN)

    assert sorted(added) == sorted(setup.CLAUDE_HOOK_EVENTS)
    for event in setup.CLAUDE_HOOK_EVENTS:
        assert ENDLESS_CMD in _commands(settings, event)


def test_repair_is_idempotent():
    """It runs on every setup invocation, so a second pass must be a no-op —
    otherwise the settings file grows a duplicate entry per run."""
    settings = {"hooks": {}}
    setup._repair_missing_hook_events(settings, HOOK_BIN)

    assert setup._repair_missing_hook_events(settings, HOOK_BIN) == []
    for event in setup.CLAUDE_HOOK_EVENTS:
        assert _commands(settings, event).count(ENDLESS_CMD) == 1


def test_pretooluse_is_synchronous():
    """PreToolUse carries every blocking gate; async would make them all inert."""
    assert "PreToolUse" in setup.SYNC_EVENTS
