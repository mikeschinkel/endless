E-972 fixed event_bridge.emit_event so events now correctly land in main's .endless/events/. But before the fix, events written from inside worktrees went to <worktree>/.endless/events/events-*.jsonl. Those stray events are not yet reflected in main's authoritative event log.

Scope: identify worktrees with stray event files (compare timestamps/contents against main), determine which events are duplicates of main vs. unique writes that need to be replayed/merged, and either replay them into main's log or document them as accepted loss.

For the e-967 worktree specifically: it may have unique events between when it was created and when E-972's fix landed; re-measure both event files at pickup time before deciding.