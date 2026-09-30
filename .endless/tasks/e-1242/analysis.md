Fix: when claim_item runs and current session does NOT resolve, scan tmux window for live Claude companions (via _read_live_companions + tmux list-panes).

If exactly 1 sibling Claude session: bind via a new 'task.claimed' event handled by Go executor.

If 0: refuse (claim is meaningless without a session) UNLESS --force (manual-work-without-Claude escape).

If 2+: refuse with message pointing at follow-up design ticket.

The hook path remains as fallback; this just makes the CLI usable from sibling panes.
