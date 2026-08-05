# E-1892 — `session monitor` re-resolves its focal task until one appears

## Scope

`internal/sessionstatuscmd` only. The monitor's resolution lifecycle — nothing
about rendering, sizing, or the layout E-1851 builds.

## Decision: re-anchor the WHOLE set, freeze on the first focal hit

Re-resolve `focal`, `parentSession` and `emittingSession` together on each tick
while `focal == 0`; stop re-resolving the moment `focal != 0`.

All three are read from the same not-yet-settled state and share one race:
`parentSession` comes from the `@endless_spawned_by` window option, which
`spawn-launch` writes immediately before its `syscall.Exec` — the same instant
the session row does not yet exist. `emittingSession` is consulted only while
`focal == 0`, so freezing it at a stale 0 would leave the no-goal view (E-1802)
permanently empty for exactly the sessions it exists to serve.

Freezing on `focal != 0` preserves E-1698's anchor-once contract unchanged: once
a task is anchored the view stays pinned to THIS window's task as other sessions
come and go. Nothing re-resolves after that point. A session that never claims
keeps re-resolving every 2s, which is correct — that is how the view recovers
when the user finally claims.

## Mechanism

`Run` currently resolves once and passes three values into `monitorLoop`. Change
it to pass an *anchor function* that returns the triple, and have `monitorLoop`
call it each tick until focal is non-zero:

    type anchor struct{ focal, parentSession, emittingSession int64; hint string }

    // seamed as a package var so tests drive the sequence, mirroring the
    // existing `worktreeAnomalies` seam in this same file
    var resolveAnchor = func(process string) (anchor, error) { ... }

`process` is the session's process handle (`sessions.process`), sourced from
`$TMUX_PANE` or `--process`. Keep the tmux-shaped name confined to the
`monitor` helpers that genuinely take a tmux pane.

`Run`'s existing `--task` / `--session` headless branches keep their fixed
anchors (they name the focal directly, so there is nothing to wait for) — those
call `monitorLoop` with an anchor func that returns a constant.

### Decided: add a test-intended `--process`, do NOT gate the pin on `--config-dir`

`sessionstatuscmd.Run` calls `monitor.PinMainDB()` unconditionally on the normal
(process-resolved) path, so `--config-dir` cannot redirect it and the
process→session resolution cannot be exercised against a seeded DB — which is
what this task must test.

**Add `--process <id>`** to `session-status`, on the exact precedent of the
existing `--task` / `--session` flags: documented *"intended for tests"*, it
supplies the value the live path would read from `$TMUX_PANE` and, like those
two, honors the resolved DB context instead of pinning main. It still goes
through the real `ResolveSessionStatusFocal` → `GetPaneStatus` path, so it
exercises the resolution this task changes rather than bypassing it.

Named `--process`, NOT `--pane`: the column it feeds is `sessions.process`, and
a pane is a *tmux* concept. `session-status` is not a tmux-specific surface, so
tmux vocabulary must not leak into its flag names — a tmux pane id is merely
what `process` holds today, and would stop being the right word under the
multiplexer-driver seam (E-1085). `tmux status-line --pane` keeps its name
precisely because that surface IS tmux; the two flags differing is the rule
working, not an inconsistency.

**Rejected: `if !monitor.HasExplicitDBContext() { PinMainDB() }`.** Three
reasons, recorded so this is not relitigated:

1. It converts an invariant into a caller-dependent convention. "Session/pane
   state always comes from main" currently holds regardless of flags, matching
   the hook and the status line. Gated, it holds only when the caller stays
   quiet — and the failure is SILENT: the view renders the no-task hint, which
   is indistinguishable from "nothing claimed here."
2. It is production-reachable, unlike the `main.go` precedent it would borrow
   from. That rule is safe for `hook`/`channel`/`tmux` because production never
   passes `--config-dir` to those. `session-status` is invoked by a Python CLI
   that already translates `--db main|sandbox` into `--config-dir` and today
   deliberately does not thread it here — one natural refactor away from
   `endless session status --db sandbox` quietly emptying the view.
3. It adds a second orthogonal exception axis to a pin E-698 just narrowed to
   one deliberate rule ("one self_dev rule, not a per-command exception").

`--process` has none of these properties: the Python CLI never passes it, so
the production path keeps the unconditional pin exactly as it is today.

## Tests — `tests/tasks/e-1892-verify.sh`

One suite, task's own checks fail-fast first, then project-wide regression.

**Layer A — unit, FAIL-FAST** (`internal/sessionstatuscmd`)
- Drive `resolveAnchor` through the seam with a scripted sequence — returns
  `focal=0` three times, then `focal=1851` — and assert `monitorLoop` picks up
  1851 and renders its rows.
- Assert it STOPS calling the resolver after the first hit: the seam counts
  calls; the count must not advance across subsequent ticks.
- Assert `parentSession`/`emittingSession` update on the same tick focal does,
  not one tick later.
- Assert a resolver ERROR while focal is still 0 is non-fatal — the monitor
  keeps its hint and retries; it must not exit the way a render error does.
- Assert `--process` carries the same contract as `--task`/`--session`: it
  skips `PinMainDB` and reads the resolved DB context, while still routing
  through `ResolveSessionStatusFocal` rather than bypassing resolution.
- Assert the production path is UNCHANGED: with no `--process`/`--task`/
  `--session` and no self_dev worktree, the pin still fires unconditionally —
  an explicit `--config-dir` must NOT redirect it (the rejected design above,
  pinned as a regression guard so it cannot creep back in).

**Layer B — integration (isolated tmux server + seeded throwaway DB)**
The recovery proof, and what `--process` above exists to make reachable:
1. `tmux -L <socket>` private server; seed a throwaway `--config-dir` DB with a
   project + task but NO session row.
2. Split a pane running
   `endless-go --config-dir <tmp> session-status --monitor --process <that
   pane's id>` and assert it renders the no-task hint, and that E-1851's fit
   holds the pane at `monitorPaneEmptyHeight` (8 rows).
3. Mid-flight, `INSERT` the session row with `process = <that same value>` and
   `active_task_id = <task>`.
4. Within ~3 ticks assert the pane now renders the task rows AND has resized to
   the exact fit (frame lines + 1) — proving recovery end-to-end and that it
   composes with E-1851's sizing.
5. Tear down the tmux server and temp dirs on exit.

**Layer C — project-wide regression**: `go test ./...` and `just test`.

Reuse the harness shape from `tests/tasks/e-1851-verify.sh` (private `-L`
socket, geometry-addressed panes never pane indexes, throwaway `--config-dir`,
trap-based teardown) — same author conventions, no shared file.

## Out of scope

- The pane HEIGHT symptom. E-1851 already holds `monitorPaneEmptyHeight` when
  there are no rows and grows to an exact fit as rows appear. This task is only
  about the rows appearing at all.
- A monitor whose cwd is a `self_dev` worktree reads that worktree's sandbox,
  whose session rows carry no `active_task_id` (verified: the e-1851 sandbox has
  1 session row, 0 with a task). That is E-698's documented cost, tracked at
  E-1883. E-1851 sidesteps it by starting the monitor pane in the project dir;
  do not re-litigate it here.



## SUPERSEDED at implementation time — `--process` was not added

The `--process` decision above, and the regression guard paired with it, rested
on a premise that was already stale when this plan was written: that
`sessionstatuscmd.Run` calls `monitor.PinMainDB()` UNCONDITIONALLY, so
`--config-dir` cannot redirect it and process→session resolution cannot be
exercised against a seeded DB.

`c186df7d` (E-698, "restore session-status to main; move the job guard to the
trigger" — already on `main`) had changed that call to:

    if !monitor.HasExplicitDBContext() { monitor.PinMainDB() }

i.e. the E-1429/E-1700 contract that an explicit `--config-dir` beats the
env-driven main pin, pinned by `internal/monitor/db_gate_test.go`'s
`TestSelfDetectVsExplicit_MainPinRouting`.

Consequences, decided with Mike before implementing:

1. **`--process` was NOT added.** Its entire justification was the
   untestability the E-698 commit had already removed. Layer B drives the real
   `ResolveSessionStatusFocal` path with `--config-dir` and the monitor pane's
   own `$TMUX_PANE`, so no new production surface is needed.
2. **The "pin fires unconditionally / `--config-dir` must NOT redirect it"
   assertion was DROPPED.** Implementing it would mean reverting a deliberate,
   merged E-698 decision and breaking its test — out of scope for E-1892. The
   routing contract that actually holds is pinned in Layer A instead, by running
   `TestSelfDetectVsExplicit_MainPinRouting` as a must-not-regress check.

Everything else in this plan was implemented as written.

### One further deviation, test-placement only

- **Layer A tests `anchorTracker` and `monitorFrame`, not `monitorLoop`
  directly.** The tracker holds 100% of the lifecycle logic this task changed;
  `monitorFrame` is the resolve→render composition the loop repeats, split out
  so it is drivable without a terminal, a ticker, or a signal. `monitorLoop`
  itself keeps only paint/fit/tick machinery, and Layer B proves its wiring
  end-to-end against a real tmux pane — which is the recovery proof anyway.


### Rebase onto main (after the first land attempt)

`e1fa96f9` ("E-1851: start the observation panes in the project dir; floor the
empty monitor") landed on main after this branch was cut and reworked the same
functions, so `worktree land` hit a genuine content conflict. Resolved in place
by rebasing — no work duplicated or lost:

- `renderSnapshot` now returns `(rowCount, error)`. `monitorFrame` forwards that
  count, so the pane fit still distinguishes "no task rows" from "a short real
  frame". The count flipping 0 → non-zero on the tick a focal task resolves is
  exactly what regrows the pane in the same repaint the rows appear in — the two
  changes compose cleanly.
- `monitorPaneEmptyHeight = 8` DOES exist as of that commit (it did not when this
  plan was drafted, which is why an earlier revision of this section said so).
  Layer B now asserts the unresolved pane holds 8 and the recovered pane fits
  exactly at 7, so recovery is a shrink rather than a grow. Direction is
  incidental; both ends are pinned exactly.
