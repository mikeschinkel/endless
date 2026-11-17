Found while planning E-2572, which inventories every tmux interaction and
files one task per cause. Merged from E-2644, E-2645, E-2646 and E-2647 per
ED-1550: one cause, four sites. E-2519 moved the data model to the
task-session server (list-live scopes pane ids to `monitor.TasksServerUUID()`,
`session_cmd._tmux_run` addresses its socket), but these paths still take
their pane, client or window identity from the caller's own server:

1. Event resolution for `session touch`, `session snapshot add` and
   `session task add/delete`: Python sends the raw `$TMUX_PANE`
   (`session_status_cmd.py`, `session_task_cmd.py`) and
   `events/session_status.go liveSessionByProcessTx` scopes it with the
   ambient `monitor.TmuxServerUUID()`, disagreeing with list-live for any
   caller outside the task-session server and able to stamp `@server_uuid`
   on the user's default server.
2. `autospawnjob.mostRecentlyActiveClientSession` runs bare `tmux
   list-clients` on whichever server the job runner is in, and
   `triagejob.startAllowed` shares its `ResolveWindow`; the `--target-session`
   they compute is dropped by spawn-window, which always opens on the
   task-session server.
3. `session_cmd._tmux_window_pane_ids`, `_window_claimed_task` and
   `_live_window_claiming(here)` pass the caller's own `$TMUX_PANE` as `-t`
   to the task-session socket even when the caller is not in that server;
   pane ids are unique only per server (E-2196), so from a console pane `%5`
   can name an unrelated task pane. Callers: `_resolve_companion`,
   `task_cmd._list_sibling_claude_session_eids`, `session resume --rebind`.
   E-2519 introduced this, so this task cleans up E-2519.
4. `session resume --tmux-session` and `--all-tmux-sessions` call
   `endless-go resume-windows`, and `resumewindowscmd.tmuxOut` runs there with
   socket `""`, reaching the ambient or default server; only
   `tmuxstartcmd.restore` passes `tasks.Socket`.
