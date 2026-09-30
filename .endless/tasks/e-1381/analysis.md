Fix: in internal/events/session_tasks.go, change shouldRecordSessionTouch to 'return evt.Actor.SessionID != ""' (drop the ActorKind check).

Verify: endless task add via Bash/agent produces a session_tasks row.

Update the test that asserts CLI-actor produces no row — it asserted the buggy behavior.
