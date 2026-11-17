Four sites still take a pane, client or window identity from the caller's own
tmux server after E-2519 moved the data model to the task-session server. Each
moves to that server; a caller whose pane is not on it is treated as a caller
outside tmux. Nothing changes which server or session a spawn opens in:
spawn-window already opens every window in the `tasks` session of the
task-session server.

## Approach

**Go: is the caller's pane on the task-session server?** One function in
`internal/tmuxservers` answers it by comparing the socket path in `$TMUX` with
`RunningTasks().Socket`; false when `$TMUX` is unset or the server is down. It
is the Go twin of Python's `tmux_cmd.in_server`, behind a package-level
function variable so tests pin it the way `monitor.setTestTmux` pins
`tasksServerUUIDFn`.

**Event resolution.** `events/session_status.go liveSessionByProcessTx` (used by
the session_status and session_task_membership events behind `session touch`,
`session snapshot add`, `session task add` and `session task delete`) replaces
`monitor.TmuxServerUUID()` with `monitor.TasksServerUUID()` and matches only
when the caller's pane is on that server; otherwise it returns `sql.ErrNoRows`
as it does today when no server can be identified. The Python side keeps its
resolver order (`--session`, then `_current_endless_session_id`, then the raw
pane); only the pane fallback's meaning changes. The four commands document the
override as `--session N`; `--session-id` stays accepted as a second spelling
of the same Click option, so nothing that passes it breaks.

**Auto-spawn and triage.** `autospawnjob.resolveTarget` keeps one job: skip
when no terminal is attached to the task-session server. It asks that server
through `tmuxservers.RunningTasks()` and `Server.Attached()` (or `Out("list-
clients")` on that socket) instead of the bare `tmuxOutput("list-clients")`,
which goes. The `auto_spawn.target` setting, `Window.Target`, `describeTarget`
and the `--target-session` argument in `spawnArgs` are removed, and
`spawn-window` drops its `--target-session` flag. `triagejob.startAllowed` and
`realEffects.spawn` keep calling `autospawnjob.ResolveWindow`, which now
returns placement and the skip reason only. Settings fail open on an unknown
key, so a config still naming `auto_spawn.target` loads unchanged.

**Companion resolvers.** `session_cmd._tmux_window_pane_ids`,
`_window_claimed_task` and `_live_window_claiming` take the caller's pane from
one helper that returns `$TMUX_PANE` only when `_in_tasks_server()` is true,
and None otherwise, so none of them sends a console pane id to the task-session
socket.

**resume --tmux-session.** `resumewindowscmd.run` resolves
`tmuxservers.RunningTasks()` before `listWindows()` and sets the package
`socket` for the whole sweep, as `Restore` does; with the server down it emits
the not-running refusal and does nothing.

**Docs.** `docs/guide/reference.md`, the auto-spawn section: the window opens
in the task-session server's `tasks` session; the run skips while no terminal
is attached to that server; two user-level settings remain
(`auto_spawn.interval`, `auto_spawn.placement`). The four commands' `--help`
shows `--session N`.

**Tests.** Go: the resolution test pins the tasks identity and the in-server
check both ways; the auto-spawn target test asserts the socket the client
check addresses; resume-windows gets a server-down test. Python: the companion
resolver tests run with `tmux_cmd.tasks_server` stubbed and `$TMUX` pointing
at and away from that socket. No test reaches a real server.
