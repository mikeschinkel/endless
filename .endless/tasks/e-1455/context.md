What remains is detection-side: session_id_resolve() identifies a Claude pane by querying the DB for a matching row, not by reading the env vars Claude Code already exposes (CLAUDECODE=1, CLAUDE_CODE_SESSION_ID, TMUX_PANE).

This leaves a first-event-timing race (CLI runs before the first hook event has registered the pane in the DB) and unnecessary DB round-trips for current-pane identification.
