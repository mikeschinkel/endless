Everything Endless does when a turn ends hangs off the Stop case — ParseTranscript then IdleSession — but Claude Code fires StopFailure INSTEAD OF Stop when a turn dies on an API error (rate_limit, overloaded, max_output_tokens, server_error and others).

Nothing else moves it — SessionEnd fires on session termination, TouchSession never clobbers a live state, and liveness sees a pane that is still there.

The board then asserts work is in flight on a dead turn, and auto-spawn's cap throttles against sessions that are gone.
