# E-1851 — `task spawn` builds the 3-pane layout and runs `session monitor`

## Scope
Foreground `spawn` only — the `spawn-window` path. Leave `--bg` (headless, no window) and `--attach` untouched; no 3-pane layout for either.

## Target layout
One tmux window, three panes:
- **Left** (50% width, full height): the spawned Claude session. Ends **focused**.
- **Top-right**: `endless session monitor`, height sized to fit one `session status` frame + 1 line.
- **Bottom-right**: a bare interactive `$SHELL` for ad-hoc `endless` commands (the remainder of the right column).

## Mechanism
Claude must stay pane 0: it is launched by `runSpawnLaunch` (internal/spawnlaunchcmd/spawn_launch.go) via `syscall.Exec`, which replaces the pane process and cannot orchestrate panes afterward. So drive all splitting from `runSpawnWindow` (internal/spawnlaunchcmd/spawn_window.go) *after* the `new-window` call returns, via tmux subprocess commands on the existing `runTmux` path.

1. Keep the current `new-window` that runs `spawn-launch --spec <path>` as pane 0 (left / claude). Do NOT change the exec-replace or the window-variable plumbing (`windowOptionCommands`, `@endless_*`); the SessionStart binding continues to key off the window options unchanged.
2. Add thin argv-builders to internal/spawnlaunchcmd/tmux_driver.go, next to `newWindowArgs`/`setOptionArgs`, keeping ALL tmux argv construction localized in that one file so E-1810's future driver-seam refactor has a single place to hoist:
   - `splitWindowArgs(target, horizontal bool, cwd string, lengthLines int, cmd []string)` → `tmux split-window {-h|-v} -t <target> -c <cwd> [-l <lengthLines>] -- <cmd...>`
   - `selectPaneArgs(target)` → `tmux select-pane -t <target>`
   - `resizePaneArgs(target string, height int)` → `tmux resize-pane -t <target> -y <height>` (only if a post-split resize is needed beyond `-l`)
3. After `new-window` returns:
   a. `split-window -h` (50/50) off the window → top-right pane; run `endless session monitor --autosize` there with cwd = the window cwd (see Sizing for `--autosize`).
   b. `split-window -v` off that right pane → bottom-right pane; run the user's `$SHELL` (bare) with cwd = the window cwd.
   c. Size the top-right (monitor) pane per **Sizing**; the remainder goes to bottom-right.
   d. `select-pane` back to pane 0 (claude) so it ends focused.
4. Best-effort, matching today's option-setting: a split/resize/select failure must NOT fail the spawn (claude in pane 0 is load-bearing). Surface tmux stderr and continue.

## Sizing the monitor pane — `session monitor` self-sizes (`--autosize`)
There is no fixed frame height: `SessionStatusRows` has no `LIMIT` (1 legend + N rows + focal-anomaly lines, N unbounded), and at `spawn-window` time the Claude session is not yet bound, so the height cannot be computed then. So `spawn-window` does NO sizing; `session monitor` sizes its own pane at runtime under a new `--autosize` flag:
- Launch the top-right pane as `endless session monitor --autosize`. Without the flag, `session monitor` behaves exactly as today (no self-resize), so a standalone monitor a user runs in their own pane is unaffected and never fights a manual size.
- With `--autosize`, on each repaint (it already redraws only when the frame changes) compute the rendered line count (legend + rows + anomaly lines) + 1 and, when it changed, run `tmux resize-pane -y <lines+1> -t <self>` (self = `$TMUX_PANE`).
- Cap the height at ~50% of the window height so a large task tree cannot starve the bottom-right shell; past the cap the pane scrolls.

## DB routing
`session status`/`session monitor` pin the **main** DB by design: `sessionstatuscmd` calls `monitor.PinMainDB()` (session/pane state is machine-local and lives in main regardless of cwd, per E-1685) and the Python `session_status_resolve` (session_cmd.py) threads no `--config-dir`. So the monitor pane reads main — there is no sandbox routing to arrange or verify. (In a self-dev worktree the spawned session's binding is written to the sandbox while the monitor reads main, so a self-dev monitor pane may not reflect that session — expected, irrelevant to the product, out of scope here.)

## Tests — tests/tasks/e-1851-verify.sh
- **Unit (fail-fast):** in internal/spawnlaunchcmd/tmux_driver_test.go, assert the new argv-builders emit the expected `tmux split-window -h/-v … -- …`, `select-pane`, and `resize-pane` argv, including the `-l` length and `-c` cwd.
- **Integration:** run a real foreground `spawn` against an isolated tmux server (private `-L <socket>`) + throwaway project/DB (single main DB, not self-dev), then assert via `tmux list-panes -F '#{pane_index} #{pane_width}x#{pane_height} #{pane_current_command} #{?pane_active,active,}'` that there are exactly 3 panes, the left pane is ~50% width and is the active pane, the top-right runs `session monitor`, and the bottom-right is a shell. Tear down the tmux server + temp dirs on exit.
- **Focal match:** assert the monitor pane resolves the SAME focal task as the Claude pane. The mechanism is `GetActiveTaskForPane` (internal/monitor/tmux_lookup.go): pane→session via the DB, then a `listPanesInSameWindow` fallback — NOT an `@endless_task_id` window option. Assert focal-match, reading main; do NOT assert any sandbox routing.

## Ordering
This task defines the canonical pane layout; E-1850's tmux-config ride-along must conform to what lands here. Relation set: `E-1851 blocks E-1850`.
