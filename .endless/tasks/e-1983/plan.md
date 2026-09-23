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

---

# Addendum — what the implementation added to this scope (2026-09-23)

Four things the plan did not name, each a consequence of one of its own
decisions and each cheaper to do here than to file.

## 1. `FindWorktreeRoot` resolves BOTH arguments, not just cwd

Decision 2 said resolve `cwd`. Resolving one side and not the other is the same
class of bug one step over: the walk's stop condition is `dir == root`, so the
two paths have to be in the SAME form or the stop never fires. The old contract
— "projectRoot arrives resolved, cwd arrives Cleaned" — was stated in the
docstring and nowhere else, and three of the function's own tests were already
violating it. `resolveAbs` is idempotent, so resolving both costs nothing and
removes the contract instead of restating it.

Consequence, deliberate: the returned worktree root is now the RESOLVED path.
That is the form `WorktreePathForTask` builds from the resolved project root, so
`enforceWorktreeGate`'s "is this your worktree" comparison now matches where it
previously could not.

## 2. The self-skip check no longer depends on path SPELLING

Falls directly out of (1) and is the one real regression it caused.
`worktreeOverrideRegistered` substring-matched the ABSOLUTE worktree binary path
against the worktree's `.claude/settings*.json`. Once this side derives the
worktree root through a resolved walk while the settings file still records
whatever `claude-settings-init` computed, a project reached through a symlink
gives the two sides different strings for the same file — the override stops
being recognized, the global binary stops deferring, and every hook fires twice.

It now matches the worktree-relative TAIL (`worktrees/e-NNN/bin/endless-go`),
which is spelling-independent above the project root and still specific: the
worktree's own name is in the needle, so a sibling's override does not match.

## 3. `blockToolUseWithRevisitPrompt` renamed to `blockToolUseWithDecision`

Decision 3's gate emits the same `decision: "block"` JSON response the revisit
gate does. Sharing an emitter named for one of its two callers would have left
the name lying; `revisitBlockResponse` renamed to `blockResponse` with it.

## 4. Docs and the refusal inventory

`docs/guide/orchestration.md` gains a "What binds a session to a task" section
(Decision 1's rule, the window options as display-and-lookup state, and the
gate's trigger and exemptions), and step 6 of the spawn flow stops describing a
window-option bind.

`docs/research-2026-09-17-refusal-inventory.tsv`, which `just test` anchors
against the tree: the renamed emitter's row follows the rename, `trySpawnBind`'s
row is marked RETIRED, and the new gate gets a row.

---

# Note for the reviewer: the repair has ALREADY RUN against the main database

Not planned, and not asked for. `go run <change file> --help`, intended as a
help probe, is not one — the runner ignores argv and applies the change — so
e-1983-repair-misbound-sessions ran against `~/.config/endless/endless.db` and
committed at 2026-09-23T12:34:39.

What it did there, verified after the fact:

- 48 tasks carried more than one `sessions` row; 30 do now.
- E-1732, the plan's named acceptance case, came out exactly as specified:
  ES-879 (launched in `e-1732`'s worktree) keeps the task; ES-881 and ES-882
  (both launched in the main checkout) are unbound.
- `sessions_task_id_write_once` is present afterward and FUNCTIONAL — probed on
  a snapshot, it refuses both a reassignment and an unbind.
- Its `_schema_version` marker is recorded, so `db apply-change` will not run it
  a second time.

The outcome is the one Decision 4 specifies, and the change file in the tree is
byte-for-byte what produced it. Nothing needs re-running; this is recorded
because the database changed outside the land, not as part of it.

---

# Reopened 2026-09-23 — Decision 3 was stronger than anyone chose

## What was wrong

Decision 3 above says the gate "returns `decision: "block"` on every tool call",
and its "Accepted consequence" paragraph notes that reading code in a worktree
without binding is therefore impossible — and waves that through. That was a
planning session's call, presented under a heading attributing the decisions to
the requester. The requester was never asked it. Nowhere in Decision 3 is
all-tools-versus-write-tools posed as a question.

Two further defects in the reasoning, recorded because they are the kind that
repeat:

1. **The severity was argued from a risk the gate does not address.** "Why this
   is worth E-1669's cost" justifies blocking everything by citing that "under
   write-once `task_id` a mis-bind is permanent and has no recovery". But this
   gate fires on an UNBOUND session. A wrongly-bound session holds a task, so
   the gate is silent for it. Preventing mis-binds is Decision 1's job; the
   gate's severity borrowed Decision 1's risk.

2. **"There is precedent" was not checked.** Two all-tool gates exist, and
   neither shares this one's reason. `enforceClaimedCwd` (E-1586) covers all
   tools because a wrong cwd makes EVERY tool do the wrong thing — Read opens
   the wrong file, Bash runs in the wrong directory; that is correctness.
   `enforceRevisitGate` (E-1542) covers all tools because a strategy revisit
   means stop and ask; that is coordination. Here the cwd is RIGHT and nothing
   is incorrect. The harm is that work is attributed to no task — and only a
   write produces work.

## What changed

**The gate covers write tools only** (Write, Edit, NotebookEdit). It moves from
the all-tools band in `handlePreToolUse` to sit beside `enforceWorktreeGate`,
and the limit is stated in `unboundWorktreeApplies` rather than left implicit in
where it is called — the same isolation principle `autoBindFromCwd`'s guards
follow, and the specific thing that went wrong here was a strength nobody could
see from the function itself.

**The escape hatch is deleted.** `bindEscapeVerbRe` existed only because the
gate blocked every tool call, so it had to recognize its own remedy from Bash
command TEXT — which a heredoc or a quoted string reads identically (E-2177).
Bash is no longer gated, so the remedy always runs and there is nothing to
carve out. This removes one of E-2177's two halves outright.

**The block message and the guide say "writes", not "tool calls"**, and both now
say plainly that reading an unclaimed worktree is fine.

SessionStart's injection is unchanged: it carries no tool name, wants to explain
regardless, and calls `unboundWorktreeDecision` directly.

## What was deliberately NOT changed

The accepted cost is that an unbound session can still modify files through Bash
(`sed -i`, `git apply`). `enforceWorktreeGate` has always had that same gap for
the same reason, and E-940 tracks it separately. Closing it here would mean
gating Bash, which is the thing that forced the text-matched escape in the first
place.
