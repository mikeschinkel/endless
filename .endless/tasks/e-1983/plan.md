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

## Decision 3 — a PreToolUse gate, scoped to unbound-inside-a-worktree

The gate ships, and its trigger is narrow: **cwd looks like a task worktree and
the session holds no task.** That is the only state left after Decisions 1 and 2
where something is genuinely wrong, and today it is silent.

It is the mirror of `enforceClaimedCwd`, which blocks when a session HOLDS a
task and its cwd has drifted out of that task's worktree — and which returns
early on `session.TaskID == nil`, leaving exactly this gap. The pair then covers
both halves: bound-but-wrong-place, and right-place-but-unbound.

**Trigger, precisely.** `TaskIDFromWorktreePath(cwd)` yields an `E-NNN`. That is
a pure regex on the path, independent of the `.endless/worktree.json` walk that
`FindWorktreeRoot` performs — which matters, because the walk is the thing that
failed. The path shape says "this is task N's worktree" while the session holds
nothing, so the two signals disagree and the disagreement is the defect.

**What it does.** SessionStart injects the explanation (its only lever), and
PreToolUse returns `decision: "block"` on every tool call until the session is
bound. The instruction rides in both `reason` and `additionalContext`, as
`preToolUseBlock` already does.

The message names which step failed, because the fixes differ: no project
resolved, no `.endless/worktree.json` found walking up from cwd, or a worktree
root whose path yields no `e-NNN`. It also names the way out —
`endless task claim E-NNN` or `endless task bind E-NNN` — so the block is
escapable by the agent reading it.

**Deliberately NOT blocked:**

- **cwd in the main checkout.** An unbound session there is the correct outcome
  per the rule above, not a breach. `enforceWorktreeGate` already blocks the
  different case of a session that HOLDS a task while sitting in main.
- **A foreign or unrelated tree** inside the project. `enforceWorktreeGate`
  leaves those alone on purpose; this does too.
- **No project resolved.** Matching `enforceWorktreeGate`'s own precedent:
  without a project root we cannot evaluate, so the call proceeds.
- **Agent-tool subagents** (`payload.AgentID != ""`). They share the parent's
  cwd but have their own session identity and are deliberately never bound
  (E-1300). Without this screen the gate would block every subagent tool call
  in every worktree — the single worst false positive available here.

**Accepted consequence.** Opening a session in a worktree merely to read code,
without binding, is no longer a usable state: the gate blocks until you bind.
That is the invariant being enforced rather than an oversight. `task bind` is
the one-command answer, and the block names it.

**Why this is worth E-1669's cost.** E-1669 chose "a warning, NOT a refuse"
because refusing in the hook path blocks every tool call. That judgment stands
for its risk. It is overridden here for a narrower one: the trigger fires only
on a state that is already broken, the population is small after Decision 2
removes the known cause, and the remedy is a single command the message names.

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

**Go — the unbound-in-a-worktree gate (Decision 3).** The SessionStart
injection plus a PreToolUse block, sited beside `enforceWorktreeGate` and
`enforceClaimedCwd` and screening subagents before anything else.

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
- The gate blocks a session sitting in a worktree with no task bound, and the
  block names both the failed step and `task bind`.
- The gate does NOT block: an unbound session in the main checkout, a foreign
  tree inside the project, or a session whose project does not resolve.
- **The gate does NOT block Agent-tool subagents.** Drive a real subagent inside
  a worktree and assert its tool calls pass. This is the false positive that
  would make the gate unusable, and it is the check most likely to be forgotten.
- The repair: run against a copy of the real database, confirm E-1732 keeps
  ES-879 and unbinds ES-882, confirm a second run is a no-op, and confirm the
  trigger is present and functional afterward.
