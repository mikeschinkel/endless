Recomputed on tmux hook events (`client-active`, `session-window-changed`, `pane-focus-in`) — would need a small daemon-like helper or a tmux command-alias that calls `endless-tmux refresh`.

Tmux `status` option is server-scoped so the recompute needs a global view of all visible panes, not just the focused one.
