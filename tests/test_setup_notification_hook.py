"""The Notification hook is installed, and installed async (E-2091).

Endless hooked six Claude Code events and none of them reported that the harness
was asking the USER something, so a session sitting on a permission prompt read
`working` — indistinguishable from one doing work. `Notification` is the event
that reports it, and installing it is the producer half of `project status`'s
first rank.

Two properties, and the second matters as much as the first. It must be in
CLAUDE_HOOK_EVENTS (or nothing fires at all), and it must NOT be in SYNC_EVENTS:
every member of that set is there because something downstream reads what it
wrote within the same turn, and the Notification handler records a session state
and gates nothing.
"""

from endless import setup

HOOK_BIN = "/usr/local/bin/endless-go"
ENDLESS_CMD = HOOK_BIN + " hook claude"


def _entry(command=ENDLESS_CMD, is_async=True):
    return {"hooks": [{"type": "command", "command": command, "async": is_async}]}


def test_notification_is_hooked():
    assert "Notification" in setup.CLAUDE_HOOK_EVENTS, (
        "without this event nothing observes a permission prompt, and a blocked "
        "session reads `working`"
    )


def test_notification_is_async():
    """It records a state and gates nothing, so there is no response for Claude
    to read and no reason to hold the harness while it runs."""
    assert "Notification" not in setup.SYNC_EVENTS


def test_an_install_predating_the_event_is_repaired_to_include_it():
    """The upgrade path, and the reason nothing extra had to be written for it.

    `_has_endless_hook` returns True on finding endless-go under ANY event, so
    `setup_claude_hook` early-returns and a machine whose install predates an
    event never gains it — reporting itself correctly set up while the event
    silently does nothing. `_repair_missing_hook_events` exists for exactly that
    case (E-2073); this is the assertion that it covers this one.
    """
    settings = {
        "hooks": {
            event: [_entry(is_async=event not in setup.SYNC_EVENTS)]
            for event in setup.CLAUDE_HOOK_EVENTS
            if event != "Notification"
        }
    }
    assert setup._has_endless_hook(settings), "precondition: setup would early-return"

    added = setup._repair_missing_hook_events(settings, HOOK_BIN)

    assert added == ["Notification"]
    hook = settings["hooks"]["Notification"][0]["hooks"][0]
    assert hook["command"] == ENDLESS_CMD
    assert hook["async"] is True
