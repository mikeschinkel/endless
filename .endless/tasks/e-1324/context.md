Observed: session_statuses row 7 inserted at 20:52:48; the CLI subprocess kept Bash tool in "Channeling" state for 2m15s before manual interrupt.

Markdown was already in Bash stdout buffer (visible 46 lines of output) before the hang.

Likely culprit: event_bridge.emit_event uses subprocess.run(cmd, capture_output=True) which blocks on full stdout/stderr drain; if endless-event holds an inherited file descriptor open (Go connection pool, sqlite shutdown, etc.) the Python side waits forever.
