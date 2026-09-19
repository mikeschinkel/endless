Folded in E-1035 (2026-08-25): 'rebuild-db projects task events into a temp DB but skips session events, so focus events fail the session_id FK constraint when replayed. Add ensureSession() to projector.go (mirroring ensureProject) that creates a stub session row when a focus event references one not yet present. Surfaced during E-1030 verification.'

Same class as this task's (a) — the projector meets a reference the projection
does not contain — and the same fix shape as the existing ensureProject. Kept
here rather than as a sibling so the missing-reference problem has one owner.

Note the interaction with E-910: if session mutations ever reach the ledger, the
stub-session workaround stops being needed for focus events specifically. It is
still needed for the pre-E-808 backfill gap, which no future emission fixes.