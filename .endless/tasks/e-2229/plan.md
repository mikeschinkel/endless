# Plan: gate monitor work on tmux window visibility

Design source: ED-1606 and the E-2223 outcome. Builds on E-2228, which removes the per-tick waste that exists whether or not a pane is visible.

## Settled while planning (Mike, in the E-2227 coordinator session)

- **Hooks are installed at runtime, as a short-term hack.**
  - The monitor installs them into the tmux server it runs on (`$TMUX`), and so does `endless tmux apply`. There is no dependency on the Endless tmux server or config (E-1850, E-2086, E-1809). Those are now relates-to, not blockers.
  - **This does NOT amend ED-1607.** The Endless-owned config file stays the intended home for these hooks. Mark the install code with a comment saying it is a stopgap until E-1850/E-2086 land and that ED-1607 still governs.
- **A visible monitor keeps today's full recompute every tick**, including per-row `git status` (with `--no-optional-locks` from E-2228). The cost of `git status` is the worktree scan, not the exec, and Mike chose freshness for the one or two visible panes.
  - So the index/HEAD-mtime and `PRAGMA data_version` change detection named in ED-1606 is not built here: with git running every visible tick, it would save little.
  - The liveness refresh (`RefreshLiveness`) also stays per tick while visible. It is per-process TEMP observation by design (E-1898), and hidden panes skip it entirely.
- **A hidden monitor does nothing:** no frame, no DB query, no git, no liveness, no job trigger.

## Visibility

A monitor is visible when its window is the active window of some client attached to its tmux server. macOS app focus is irrelevant, and several monitors may be visible at once. Outside tmux (`$TMUX_PANE` empty) a monitor is always visible, which is today's behavior.

The check, in a new small package (for example `internal/monitorvis`) built from argv builders in the style of `monitor/tmux_pane.go`:
- One `tmux list-clients -F '#{client_session} #{window_id}'`.
- One lookup of the monitor's own `#{window_id}` for `$TMUX_PANE`, re-read on each check because a pane can be moved to another window.
- Visible means the own window id is in the client set.

## Wake mechanism

- Each monitor records its pid on its pane as a pane option (for example `@endless_monitor_pid`). `sessionmonitorcmd.Tag` already tags the pane, so extend it. `project monitor` tags its pane the same way. It currently leaves `Pane` unset only to skip pane fit (E-2156), so tagging must not re-enable fit.
- A new `endless-go` subcommand (for example `monitor-wake`) is the hook target:
  - It lists panes carrying the pid option.
  - It computes the visible window set once.
  - It sends SIGUSR1 to the monitors in visible windows and SIGUSR2 to the rest, so a hidden monitor does no tmux exec of its own on a window switch.
  - It must be fast and must never fail loudly inside tmux: errors go to faults (below), not to the tmux message line.
- Hooks: `session-window-changed`, `client-session-changed`, `client-attached` and `client-detached`, each running `run-shell -b '<endless-go> monitor-wake'`.

## Hook install (stopgap)

- Install with `set-hook -ga <hook> ...`, which appends to the hook's array so a user's own hooks are neither replaced nor replaceable by ours. Make it idempotent: read `show-hooks -g` and append only when our command (recognizable by a marker, the `monitor-wake` subcommand name) is absent.
- Called from:
  - monitor startup;
  - every safety-net poll;
  - `buildApplySteps` in `internal/tmuxcmd/apply.go`.
- Removal is not needed for now. `nav_hook_retire.go` shows the retire pattern if a later task needs it.

## Loop changes (`internal/liveview/liveview.go`)

`Loop` gains a visibility state, fed by a `LoopConfig` visibility source so tests can inject it.

**Hidden:**
- Stop the ticker.
- Select only on SIGUSR1/SIGUSR2, termination signals, SIGWINCH and a safety-net timer (about 20s, inside the 15–30s range ED-1606 set).
- On the safety-net timer:
  - run the visibility check;
  - check that the hooks are installed and reinstall them if not;
  - run the binary-replacement watch (`restart.go`).
- No frame, DB query, git, liveness or `fireJobs`.

**Becoming visible**, via SIGUSR1 or a safety-net check that disagrees with the current state:
- Recompute and repaint immediately.
- Fire jobs.
- Resume the ticker.

**Visible:** today's tick (as reduced by E-2228), plus the safety-net check on the same ~20s timer, so a missed SIGUSR2 still gets corrected.

**SIGUSR2 while visible:** go hidden and leave the last frame on screen. Nobody can see it, and it repaints on return.

Apply the same gating to `project monitor` (`internal/projectstatuscmd/project_status.go`).

Jobs fire only from visible monitors. ED-1606 accepts that jobs don't run while no monitor is visible.

## Errors

New codes in `internal/faults/codes.go`, recorded with `faults.Record`:
- the `monitor-wake` hook command failing;
- hooks found missing at a safety-net poll and reinstalled;
- a hook install that fails.

A single missed hook (the safety-net check disagrees with the last signalled state) is recorded with `faults.LogOnly`, not as a fault row, because it corrects itself.

## Acceptance

Measured with E-2228's `ENDLESS_MONITOR_PROFILE` diagnostic:
- A hidden monitor uses under 0.1% CPU.
- A hidden monitor spawns zero child processes between safety-net polls, and at most the visibility check's tmux execs per poll.
- Switching to a monitor's window repaints it within about a second.
- Switching away makes it go hidden at once.
- With hooks removed by hand (`set-hook -gu`), the next safety-net poll reinstalls them and records a fault.
- Mike's manual check: 30 or more Endless windows, each with a session monitor, and the machine above 90% idle.

## Tests

- `liveview.Loop` state machine with an injected visibility source and injected signals, using the fast `Tick` the existing restart tests use:
  - hidden does no `Render`, no `FireJobs` and no ticks;
  - SIGUSR1 repaints at once;
  - safety-net disagreement flips the state.
- The visibility check and the hook-install argv builders, including idempotency when our hook is already present among a user's hooks.
- `monitor-wake` signals the right pids for a given client and window set (a pure function over parsed tmux output).
- An end-to-end case on the verify suite's private tmux server (the E-1851 isolation): two windows, two monitors, `select-window` flips which one renders.
- `just test-go` and `just test` green.


## As built (where the implementation departs from or adds to the above)

- **Tag option is `@endless_monitor_upid`, holding the monitor's UPID**, not a bare pid. `monitor-wake` signals a tag only while `UPID.Alive()`, so a tag outliving its monitor never signals a process that reused the pid.
- **One tmux exec per safety-net poll.** The pane's window, every client's window and the four hooks are read in one chained invocation; the hook check costs nothing extra. Measured: a hidden poll is `git=0 tmux=1`, and a hidden monitor used 0.042% CPU over 24s.
- **A missed hook is a `monitor-visibility` line in `user-machine.jsonl`** (`monitor.LogMonitorMissedHook`). `faults.LogOnly` is a reader, not a writer, and a log-only detail line would surface on the fault row as an unread notice, which is what "only logged" was meant to avoid.
- **WARN-0033 `monitor-visibility-unreadable`** covers both the `monitor-wake` hook failing and a monitor's own poll failing. A monitor that cannot ask counts as visible (it draws instead of hiding from someone). **WARN-0034** `monitor-hooks-missing`, **WARN-0035** `monitor-hooks-install-failed` as planned.
- **Concurrent installs are deduplicated.** Monitors started together, such as a restored server's windows, each found the hooks missing and each appended them; the end-to-end test caught two copies. After each install a monitor reads the hooks back and unsets every entry of ours after the lowest index. The last installer's read-back sees every copy, so the copies converge to one. Every poll also deduplicates.
- **`endless tmux apply` installs the hooks from `runApply`, after `retireNavHooks`**, not as `buildApplySteps` entries. An idempotent install has to read the live server, which is the reason `retireNavHooks` is not a step either, and the retire's `set-hook -gu` clears a hook's whole array, ours included.
- **SIGUSR1/SIGUSR2 are ignored across a restart's exec** (E-2193), so a wake landing before the new image subscribes is dropped instead of killing it. The tag survives the exec, because the UPID does.
- `monitor-wake` is pinned to the main database like `tmux`, since tmux invokes it. Its only write is a fault.
- The profile's tick line gains `state=visible|hidden|poll`.
- Tests: `liveview` state machine (`visibility_test.go`); `monitorvis` unit tests over a fake tmux; and real-tmux end-to-end tests in `monitorvis/e2e_test.go` using a helper built from `testdata/visprobe`, a private server and a control-mode client. They check that `select-window` moves the work, that removed hooks are reinstalled, and that `EnsureHooks` is idempotent beside a user's own hook. The verify suite repeats the switch with the real `endless-go session-status --monitor`.

- **A wake hook whose binary is gone is stale.** It was installed by an `endless-go` since moved or deleted, such as a removed worktree's `bin/`. A poll (or `tmux apply`) unsets it and installs one naming its own binary, raising WARN-0034 as for a missing hook. Without this, the marker (the verb, not the path) made a dead hook count as installed, and every switch ran a command that could only fail.
