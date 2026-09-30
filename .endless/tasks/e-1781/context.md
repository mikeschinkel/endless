session activity (src/endless/session_activity.py + cli.py registration + tests) reports per-session work projected from the event ledger.

Dependency check found no consumers: the only non-test references are its own module and CLI registration; worktree_cmd.py mentions are an unrelated commit-message string, the Go 'activity' hits are last_activity idle-throttling, and event_bridge.py is only a comment noting actor.session_id enables activity filtering (session_id on events has independent uses and stays).
