Three subcommands: list (table of recent rows; filter by --session N or --task E-NNN; --limit N); show <id> (render the full row as markdown via the same renderer used by add); latest (convenience for the latest row for this session, defaulting via tmux pane lookup).

All reads go through Go per E-894; Python is parse-args + display only.

Likely needs a new monitor function or two for the underlying queries (RecentSessionStatuses, SessionStatusByID).
