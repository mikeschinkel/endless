# Plan — spawn only onto `endless-tasks`; add `endless stop`

Decided with Mike, 2026-10-17, in the E-2300 planning session.

## 1. No fallback off Endless's servers

- `internal/spawnlaunchcmd/spawn_window.go`: when `tmuxservers.RunningTasks()` reports `endless-tasks` not running, refuse before anything is created: "Endless isn't running. Run `endless start`, then retry." Today it falls through to the caller's own server.
- `--tmux-session` names a session on `endless-tasks` only, never on the caller's server; the same refusal applies when that server is not running.
- `src/endless/task_cmd.py`: the two E-2240 gates (the claim's no-session check and the spawn's "Not in a tmux session" check) stop accepting `$TMUX` as an alternative. Both require `tmux_cmd.tasks_server()` and give the same "Endless isn't running" refusal.
- Auto-spawn (E-1814) gets the same refusal; the job reports it in the monitor and spawns nothing. It does not start a server.
- Nothing starts a server as a side effect: only `endless start` does.

## 2. `endless stop`

`endless stop tasks`, `endless stop console`, and `endless stop all` (also spelled `endless stop --all`), behind `endless tmux stop` like `start` (`tmux_start` in `src/endless/cli.py`), implemented in Go next to `internal/tmuxstartcmd` (`endless-go tmux stop`). One argument is required; none prints usage.

**`stop tasks`:**
1. Refuse while any session bound to a pane on `endless-tasks` is `working`, listing those sessions (E-N, session id, state). `--force` skips this refusal.
2. Freeze the restore record (`tmux-task-windows.json`) so the `window-linked`/`window-unlinked`/`window-renamed` hooks cannot rewrite it while windows close: set a server option, e.g. `@endless_stopping`, that `RunRecord` checks and honours by writing nothing. The record therefore holds the windows open, in order, when stop began.
3. Type `/exit` and Enter into each pane running Claude (`send-keys`), so each Claude quits through its own exit path and Endless records its session as ended.
4. Wait for those panes to stop running Claude, up to a timeout. Any still running at the timeout are listed and the server is left running (`--force` kills it anyway). Otherwise `kill-server`.

**`stop console`:** `kill-server` on `endless-console`. It holds project monitors and shells, no Claude sessions.

**`stop all`:** `stop tasks`, then `stop console`; the console is not stopped when the tasks stop refuses or leaves the server running.

A server that is not running is reported as already stopped, not an error.

## 3. Restore keeps window order

`internal/tmuxservers/state.go` `normalizeIDs` sorts the recorded ids numerically, so the record loses the windows' order and `restore` in `internal/tmuxstartcmd/start.go` recreates them by task id. Fix:
- `TaskWindows` lists windows by `#{window_index}` and the record keeps that order (dedupe keeping the first).
- `WriteState` treats a change of order as a change, so a reordered tab is recorded.
- `restore` recreates missing windows in recorded order, then moves task windows so the final order matches the record, including windows that were already open.

## 4. Default terminal when none is configured or detected

`internal/tmuxservers/terminal.go`, after the configured launcher and the detected one:
- macOS: Terminal.app, which every Mac has, via the existing `apple_terminal` launcher.
- Linux: `xdg-terminal-exec` if on PATH, else `x-terminal-emulator` (Debian/Ubuntu alternatives) if on PATH.
- Otherwise print the attach commands, as today.

Windows: tmux has no native Windows build, so Endless on Windows means WSL, which takes the Linux path. Opening a Windows Terminal window from WSL is out of scope.

## Tests

Go tests under the verify harness's private socket directory (E-2240's `tmux.socket_dir` override): spawn refusal with the server down; `--tmux-session` targeting `endless-tasks`; `stop tasks` refusal while working, `/exit` sent, record frozen, server killed; `stop console`; `stop all` order and refusal propagation; record order preserved across stop/start; launcher fallback selection as a pure function per GOOS and PATH. Python tests for both `task_cmd.py` gates.
