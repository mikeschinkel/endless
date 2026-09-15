"""The StopFailure hook is installed, and installed async (E-2145).

`Stop` and `StopFailure` are the two ways a Claude Code turn can end, and the
harness fires one or the other — `StopFailure` when the turn dies on an API
error (rate_limit, overloaded, max_output_tokens, server_error and the rest).
Endless hung every end-of-turn action on `Stop`, so a failed turn took none of
them: the transcript went unparsed and the session sat at `working` on a dead
turn, which `project monitor` renders as work in flight and auto-spawn's cap
counts as a live claim on the user's attention.

Three properties here, and the third is the one that would be easy to lose.

1. It must be in CLAUDE_HOOK_EVENTS, or nothing fires at all.
2. It must NOT be in SYNC_EVENTS: the hooks reference is explicit that Claude
   Code does not read this hook's output on any exit code, so a synchronous
   entry would buy nothing and delay an already-failed turn to do it.
3. An install predating the event must be repaired to include it, or every
   machine already running Endless keeps the bug forever while reporting itself
   correctly set up.

Installing is necessary and NOT sufficient — the handler is what makes the
session honest, and that half is asserted in Go (internal/hookcmd). This file
covers only the install.
"""

from endless import setup

HOOK_BIN = "/usr/local/bin/endless-go"
ENDLESS_CMD = HOOK_BIN + " hook claude"


def _entry(command=ENDLESS_CMD, is_async=True):
    return {"hooks": [{"type": "command", "command": command, "async": is_async}]}


def test_stopfailure_is_hooked():
    assert "StopFailure" in setup.CLAUDE_HOOK_EVENTS, (
        "without this event nothing observes a turn that died on an API error, "
        "and the session reads `working` on a dead turn indefinitely"
    )


def test_stopfailure_is_async():
    """Claude Code does not read this hook's output on any exit code — no JSON,
    no decision, exit 2 ignored — so there is nothing to hold the harness for."""
    assert "StopFailure" not in setup.SYNC_EVENTS


def test_stop_is_still_hooked_and_still_sync():
    """StopFailure is Stop's alternative, not its replacement. A turn that ends
    normally must keep the handling it has always had, including the synchronous
    entry E-1901 established for the relay gate."""
    assert "Stop" in setup.CLAUDE_HOOK_EVENTS
    assert "Stop" in setup.SYNC_EVENTS


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
            if event != "StopFailure"
        }
    }
    assert setup._has_endless_hook(settings), "precondition: setup would early-return"

    added = setup._repair_missing_hook_events(settings, HOOK_BIN)

    assert added == ["StopFailure"]
    hook = settings["hooks"]["StopFailure"][0]["hooks"][0]
    assert hook["command"] == ENDLESS_CMD
    assert hook["async"] is True
