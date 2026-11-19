# Write each server's socket into its generated tmux commands

## Generated lines (`internal/tmuxcmd`)

Change `buildApplySteps` to take the target server and write its socket into the `status-line` and `show-menu` lines. Change `buildMenuItems` to do the same for `active-id` and `tmux display-popup`. Add `--socket` to `status-line`, `show-menu` and `active-id`. One resolver in `tmuxservers` serves these three and `monitor-wake`: `--socket` when given, else the configured server whose socket matches `$TMUX`, else an error the verb turns into its own failure.

## Pane lookups (`internal/monitor/tmux_lookup.go`, `processes.go`)

Pass the server as a parameter through `GetPaneStatus`, `GetTaskForPane`, `queryTaskForPanes`, `anySessionForPanes`, `listPanesInSameWindow` and `paneIsRunningShell`, resolving identity through `processIDsForPanesOn`. Callers outside this task keep a caller's-server path.

## apply and init

`runApply` and `runInit` take a required `--server tasks|console`, resolve it through `tmuxservers.Resolve`, and run every tmux call through that `Server`, including `retireNavHooks` and `refresh-client`. `Server.Init` passes `--server` and stops setting `$TMUX`. `src/endless/cli.py` and `src/endless/tmux_cmd.py` expose the flag. `tmuxstartcmd` re-applies the lines when it reuses a running server.

## spawn-launch

`runSpawnWindow` adds `--socket` to the window command. `runSpawnLaunch` requires it and sets `tmuxSocket` from it.

## monitor-wake hooks (`internal/tmuxservers/conf.go`, `internal/monitorvis`)

`confBody` declares the four `HookNames` for both roles with `set-hook -ga`. `RunWake` takes `--socket` and uses it for its tmux calls and wake files. Remove `InstallHooks`, `EnsureHooks`, `installMissing` and the hook handling in `Gate.poll`, and retire the two hook fault codes.

## Monitor refusal

`session-status --monitor` and `project-status --monitor` refuse before the live loop unless `$TMUX` matches a configured socket. Piped single-frame output is unchanged.
