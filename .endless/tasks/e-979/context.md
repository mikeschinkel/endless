E-972 fixed event_bridge.emit_event so events now correctly land in main's .endless/events/. But before the fix, events written from inside worktrees went to <worktree>/.endless/events/events-*.jsonl.

Those stray events are not yet reflected in main's authoritative event log.

The e-967 worktree-specific evidence is in the analysis field.
