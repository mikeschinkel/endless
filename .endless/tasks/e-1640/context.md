Tasks accrue many sessions rows because the dedup key is session_id (Claude UUID), not active_task_id, and the only dedup path (collision-invalidation by tmux pane) is inert when TMUX_PANE is empty.

Each launch INSERTs a fresh-UUID ghost row.
