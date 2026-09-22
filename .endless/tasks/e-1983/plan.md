# Plan: the worktree directory is the only thing that binds a session to a task

Mike's decisions, 2026-09-21/22. No open questions.

## The rule

**A session's task comes from its working directory. Nothing else binds.**

`@endless_task_id` stops being an authority for binding and becomes what it
already is for every other reader: display and lookup state (the status line's
focal-task fallback, `endless tmux task` per E-1302, the window name).

Three consequences, all intended:

- A `claude` started in the MAIN CHECKOUT is never bound to a task, whatever
  window it is in. An unbound session is a normal state; `task claim` / `task
  bind` is how it acquires one, and `/cd` into the worktree is the other route.
  Telling the user to `/cd` before continuing is an acceptable cost.
- A `claude` started in worktree `e-A` binds to A even when the window still
  says B. The directory wins; the window is then stale and is fixed with
  `session resume --rebind` (E-2168), not by re-pointing the session.
- A spawned session still binds, because `task spawn` launches it with
  `tmux new-window -c <worktree>` and the worktree path is canonical by
  construction.

## Decision 1 — invert the precedence in the SessionStart hook

`trySpawnBind` runs first today and, on success, returns true, which suppresses
`maybeCwdBind`. That is backwards: the codebase already records cwd as the more
reliable signal. E-1700's own comment on `maybeCwdBind` says
`payload.CWD` "is the worktree for a spawned worker, so this fallback binds
reliably" — it was added precisely because the window option can race to empty.

`trySpawnBind` stops binding. `autoBindFromCwd` becomes the single bind path.

Do NOT keep it as a fallback for "cwd found nothing". That is exactly the
papering-over this task exists to remove: when cwd finds nothing, the answer is
an unbound session and a message, not a bind from a source that may be stale.

Whether the function is deleted outright or retained for its subagent screen and
its logging is an implementation choice; what must not survive is any path where
the window option determines `sessions.task_id`.

## Decision 2 — `FindWorktreeRoot` resolves cwd for symlinks (rides here)

Today `projectRoot` arrives resolved and `cwd` arrives Cleaned-but-unresolved —
stated in the function's own docstring and defended nowhere. The walk compares
`dir == root` at every step, so a single unresolved component means the stop
never fires, the walk runs to filesystem root, and the session silently fails to
bind.

The resolver already exists: `monitor.resolveAbs` / `ResolvedProjectPath`
(`internal/monitor/project_path.go`, E-2002). It is non-strict in the pathlib
sense — a missing leaf resolves as far as it exists and the tail is re-appended
— which is what makes it safe on a path that is being created.

`FindWorktreeRoot` resolves `cwd` through it before walking. This is a
correctness fix in its own right; it rides here because the inversion is what
makes the failure user-visible instead of silently covered by the window option.

E-2002's doc already names the population this affects: "any macOS project
reached through /var or /tmp (both symlinks into /private) and any user whose
projects live under a symlinked parent."

## Decision 3 — no PreToolUse gate is built

The earlier framing of this task proposed refusing the session: inform at
SessionStart, then deny every tool call at PreToolUse. Under Decisions 1 and 2
there is nothing left for it to catch.

- cwd in the main checkout: the session is unbound, which is now the CORRECT
  outcome rather than a breach.
- cwd in a worktree that disagrees with the window: the session binds from cwd,
  correctly; only the window is stale.
- cwd in a worktree that fails to resolve: Decision 2 removes the only known
  cause.

So the gate would carry E-1669's "refusing in the hook path blocks every tool
call" cost with no case to justify it. Not built. If a future case appears,
E-1669's precedent stands until something overturns it deliberately.

What ships instead is a SessionStart message (injected context, the only lever
SessionStart has) when a session lands unbound in a directory that LOOKS like it
should have bound — inside the project, not the main checkout. It names which
step failed: no project resolved, no `.endless/worktree.json` found walking up,
or a worktree root whose path yields no `e-NNN`. Those have different fixes and
a single "could not bind" is not actionable.

## Decision 4 — repair the rows this bug already created

45 tasks carry more than one `sessions` row, 127 rows total. Split by the launch
directory each row's first hook event recorded (`activity.working_dir`): 20
tasks where every row shares one launch directory (plausibly genuine clears,
E-2063's population), and 25 where rows were launched from DIFFERENT directories
(at least one was mis-bound — this task's defect).

Both populations need the same judgment, so the repair runs once, here, after
both mechanisms are fixed.

**It ships as a schema change file under `internal/schema/changes/`** that:

1. `DROP TRIGGER sessions_task_id_write_once`
2. rewrites the affected rows, printing what it decided per task
3. `CREATE TRIGGER sessions_task_id_write_once` again, byte-identical to
   schema.sql's

all in one transaction. This is the same drop-repair-recreate shape E-1969's own
change file already uses to rename around that trigger. The trigger is never
absent outside the transaction, and NO verb gains a runtime bypass — write-once
stays absolute for every caller.

Rules the repair follows:

- **The discriminator is the launch directory**, taken from the FIRST
  `activity.working_dir` for each row's UUID. Same directory as the surviving
  row means an instance of it; a different directory means a separate session
  that was mis-bound, and it is unbound rather than folded in.
- **Never keep the newest row.** Keep the row the conversation is attached to.
  E-1732 is why: ES-879 holds all 292 lines of real work and reads `ended`,
  while ES-882 — newest, `working`, and the row `task show` surfaces — holds 801
  lines of unrelated conversation. Keep-the-newest would destroy the history and
  keep the husk.
- **Fold nothing silently.** 25 of 45 need a judgment call, so the repair prints
  its decision per task and is re-runnable.

Trap that already caught one reading, recorded so it does not catch another: a
`/cd` changes a transcript's per-line cwd but does NOT move the transcript file,
which stays under the slug of the directory the session was LAUNCHED in. Only
tool-result overflow files follow the new slug. Lineage must be read from the
FIRST `working_dir`, never the last.

## Work

**Go — `internal/hookcmd/claude.go`.** Remove the binding role from
`trySpawnBind` and make `autoBindFromCwd` unconditional (it keeps its own
subagent screen and its E-1856 guard against re-pointing). The `spawnBound`
plumbing through the SessionStart branch goes with it.

**Go — `internal/monitor/worktree_lock.go`.** `FindWorktreeRoot` resolves `cwd`
via the E-2002 resolver before walking, and its docstring stops claiming cwd is
unresolved.

**Go — the SessionStart message.** Emitted only when the session lands unbound
inside the project but outside the main checkout, naming the step that failed.

**Schema change — the repair**, as Decision 4 specifies.

**Docs.** `endless guide` describes binding as cwd-derived; the window option is
documented as display and lookup state that never binds.

## Verification

- `just build`, `just test`, `just test-go` clean.
- A fresh `claude` in the main checkout, in a window whose `@endless_task_id` is
  set to another task, lands UNBOUND. This is the E-1732 reproduction and is the
  single most important check.
- A fresh `claude` in worktree `e-A`, in a window whose `@endless_task_id` says
  B, binds to A and leaves B in the window option.
- `task spawn` still binds its worker on the first event.
- A project reached through a symlinked path binds from cwd; before Decision 2
  the same case binds nothing.
- The repair: run against a copy of the real database, confirm E-1732 keeps
  ES-879 and unbinds ES-882, confirm a second run is a no-op, and confirm the
  trigger is present and functional afterward.
