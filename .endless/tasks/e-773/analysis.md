Fix: use process identifier (TMUX_PANE etc.) as the identity throughout conversations and messages tables instead of session UUIDs. Drop _resolve_session() from channel commands entirely.
