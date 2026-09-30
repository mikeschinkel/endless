Extend `internal/events/event.go` Actor struct with an optional `SessionID` field, add `--session-id` flag to `cmd/endless-event/main.go`, and have `src/endless/event_bridge.py:emit_event` read `_current_endless_session_id()` (already at `task_cmd.py`) to populate it.

Hook side (`cmd/endless-hook/claude.go`) gets the session UUID from its payload.

Existing events with no session_id remain valid.
