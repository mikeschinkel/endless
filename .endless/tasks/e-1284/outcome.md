Shipped Phase A — session_id plumbed through event emission.

Changes:
- `Actor` struct in `internal/events/event.go` gains `SessionID string` with `omitempty`. Legacy events keep their JSON shape; new events attach the field when known.
- `endless-event emit` accepts `--session-id <id>` flag.
- Python `emit_event` reads `_current_endless_session_id()` automatically (from task_cmd.py:1968) when no explicit session_id is passed. All existing CLI callers transparently pick up session attribution.
- Hook side (`cmd/endless-hook/claude.go`) unchanged — verified the hook does direct DB writes via monitor.* helpers, not event emission.

Tests:
- New TestActor_JSON_SessionIDOmitWhenEmpty + TestActor_JSON_RoundTrip in `internal/events/actor_session_test.go` cover the JSON shape + back-compat.
- 321 Python tests + Go events package tests pass.

Verification deferred to land+install: after `just install`, any `endless task add/claim/update` from this session should produce a ledger entry with `actor.session_id` matching `$ENDLESS_SESSION_ID`. Quick test:

  endless task add 'Smoke test' --description x
  tail -1 .endless/db-ledger/db-entries-*.jsonl | jq .actor

Should show `"session_id": "356"` (or current session). Phase B (E-1285) uses this field for the activity report.