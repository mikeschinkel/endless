Raised while reconciling E-2519 (EQ-254). Mike runs endless commands from endless-console shells today.

## Planning survey (2026-11-16): how each tmux site reaches its server

A starting inventory, from a read of the code; the research treats it as incomplete.

**By socket path to the task-session server:** `endless start` (tmuxstartcmd.Run, startTasks, restore via resumewindowscmd.Restore(tasks.Socket)), `stop tasks|all` (tmuxstopcmd), `endless-go tmux record-windows`, `task spawn` (task_cmd.spawn_plan → spawnlaunchcmd/spawn_window.go), `task claim`, `task prime`, `endless-go spawn-layout`, `session goto`/`back`/`goto --resume`/`--background`/`resume --new-window` (session_cmd._tmux_run), `session resume` in the current pane (_require_tasks_pane), list-live (monitor.listLiveSessions scopes pane_id to TasksServerUUID), the triage job's state and resume.

**By socket path to the console server:** `project monitor <name> --tmux` (projectstatuscmd/window.go), `start`'s console half, `stop console|all`, monitor.liveEndlessServerDown.

**Bare tmux against the caller's $TMUX server (departures from the rule):**
- `tmux apply`, `tmux init` (tmuxcmd/apply.go, init.go): require $TMUX, stamp @server_uuid and the status line on whichever server the caller is in.
- `tmux status-line`, `endless-go tmux show-menu`, `task id` / `tmux task` (tmuxcmd/status_line.go, show_menu.go, active_id.go): pane from --pane or TMUX_PANE, then monitor.GetTaskForPane / GetPaneStatus → ProcessIDsForPanes scoped by ambient TmuxServerUUID().
- `session monitor` and `project monitor` without --tmux: pane tag (sessionmonitorcmd/tag.go), visibility (monitorvis; serverKey reads $TMUX), pane fit (monitor/tmux_pane.go), session-status anchor (ResolveSessionStatusFocal, tmuxWindowOption). They run in a pane of whichever server holds them.
- `endless-go monitor-wake` (monitorvis.RunWake), run by the hooks of the server it is installed in.
- `session monitor --restart [--tmux-session|--all-tmux-sessions]` (sessionmonitorcmd/restart.go).
- `session resume --tmux-session | --all-tmux-sessions` → endless-go resume-windows with socket "" (E-2645).
- `endless-go spawn-launch` (spawn_launch.go, tmux_driver.go:spawnerSession): window options via bare tmux -t $TMUX_PANE; ambient is the tasks server by construction since it runs inside the new pane.
- Python: task_cmd._tmux_window_session_uuid (behind _current_endless_session_id: claim, bind, report, touch, snapshot, session id); task_cmd._prime_draft_window_task.
- Event resolution for `session touch`, `session snapshot add`, `session task add/delete`: Python sends raw TMUX_PANE; events/session_status.go liveSessionByProcessTx scopes with ambient TmuxServerUUID() (E-2644). Disagrees with list-live.
- `session-query ensure-claude-id --process $TMUX_PANE` (task_cmd._ensure_claude_session_id) → TouchSession → ambient TmuxServerUUID: any verb run from a Claude pane can bind or stamp the ambient server.
- autospawnjob.mostRecentlyActiveClientSession: bare `tmux list-clients` on the runner's server; triagejob.startAllowed shares ResolveWindow (E-2646).
- monitor/liveness.go:observeTmuxServers: one bare `display-message #{socket_path}`, then -S probes of the ambient, default and Endless socket directories (read-only).
- tmuxservers/terminal.go: bare `display-message #{client_termname}` when $TMUX is set.
- session_cmd._tmux_window_pane_ids, _window_claimed_task, _live_window_claiming(here): send the caller's own $TMUX_PANE to the tasks socket even from a console pane (E-2647).

**Claude hooks:** hookcmd.runClaude → monitor.TouchSession(sid, "claude", $TMUX_PANE) → EnsureTmuxPaneProcess → TmuxServerUUID() (bare show-options, stamps a UUID if unset) → EnsureProcess + recordTmuxServer(ambient #{socket_path}). Also bare -t $TMUX_PANE: setTmuxSessionUUID every event; tmuxTaskID, tmuxWindowAutoSpawned at SessionStart; BindSessionToTask from autoBindFromCwd. `hook prompt` (monitor.GetTmuxContext) stamps @session_uuid/@window_uuid/@pane_uuid on the ambient server. A Claude outside tmux reads unbound (process_id NULL). A Claude on the console or the user's default server is bound to that server's UUID and can read live, but list-live exposes no pane for it, so goto, triage and companion matching cannot see it.

**Entry points with no tmux server:** start (tmux binary only; terminal detection runs bare tmux only when $TMUX is set), register and setup (no tmux calls found), the verify runner (personEnv strips TMUX/TMUX_PANE/TMUX_TMPDIR, private TMUX_TMPDIR, fixture panes on a private server, kills its servers after), jobs (no cron; jobs.RunDue from the monitor loops and `endless jobs run`; only autospawn, triage and prime touch tmux), tests (conftest scrubs env and stubs tmux_cmd.tasks_server; Go uses internal/tmuxtest with a guard test and the seams tmuxServerUUIDFn, tasksServerUUIDFn, tmuxSocketPathFn, endlessServerDown), `endless-go event emit` (tmux only when the payload carries `process`).

**Console:** each window holds `endless project monitor <project>` above a shell; conf.go gives the console `@endless_server console` and no record-windows hooks. Console-only: `project monitor --tmux`, `stop console`, start's console half.

**No scratch or third server exists.** Settings (internal/tmuxservers/settings.go): tmux.socket_dir (the per-user Endless directory under the system tmp root), tmux.console_server (endless-console), tmux.tasks_server (endless-tasks), tmux.tasks_session (tasks), tmux.terminal (detect). The console session name `console` is a constant. ED-1607 still describes one `tmux -L endless` server with an ambient fallback; Mike: obsolete, the servers are endless-tasks and endless-console.
