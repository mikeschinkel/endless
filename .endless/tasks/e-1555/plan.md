# E-1555: Add `--reopen` to `task spawn` and `task reopen <id>` verb

## Motivation

Reopening a previously-completed task currently requires `endless task claim --force`, which binds the task to the operating session and provisions/reuses the worktree. That flow is right for **self-pickup** (the operating session is going to do the work), but wrong for **handoff to a spawn** (a fresh session should do the work).

Symptom on 2026-06-11 during E-1434 cleanup-then-spawn flow: this session ran `claim --force` to reopen E-1434 for spawn handoff. The subsequent `endless task spawn E-1434` failed with "already active in session N." The orchestrating session's binding blocked the handoff.

Two distinct needs, neither cleanly served today:

1. **Spawn-with-reopen**: "this task is `assumed`/`confirmed`/`completed`, and the new spawned session should reopen-then-work-on it." Should be one command, no orchestrator binding.
2. **Standalone reopen**: "flip the task to actionable state but don't commit to who picks it up yet." Lets an orchestrating agent reopen and then decide (spawn, claim, or hand-back to Mike).

## Scope

Add two CLI affordances. Both are additive — existing `claim --force` semantics unchanged (still correct for self-pickup).

### 1. `endless task reopen <id>`

Standalone verb. Flips a terminal-status task back to actionable state without provisioning a worktree, without binding to any session, and without emitting a claim event.

### 2. `endless task spawn <id> --reopen`

Flag on the existing `spawn` verb. When set, treats a terminal-status target as a valid spawn target by reopening it as a pre-step, then proceeding through the existing spawn flow (which binds the new spawned session, not the operating session).

## Reopen semantics (shared between the verb and the flag)

- **Reopenable from statuses**: `assumed`, `confirmed`, `completed` (epic terminal per E-1537).
- **NOT reopenable from**: `declined`, `obsolete`. Those carry explicit decisions ("we chose not to do this"); reversing them is an intentional act that should use `endless task update --status` and `--reason` semantics, not a generic reopen.
- **Target status after reopen**:
  - `ready` if the task has `tasks.text` set (a plan is attached).
  - `needs_plan` if `tasks.text` is empty.
- **Side effects**:
  - No worktree creation, deletion, or rebinding.
  - No session binding (operating session is unaffected; the task's `active session` field, if any was set previously, is cleared).
  - Emit one row in the existing task-event audit channel with the reopen transition recorded (using endless's existing status-change event mechanism).

## `endless task reopen <id>` behavior

1. Validate the task id exists (error if not found).
2. Read current status.
3. If status is in `{declined, obsolete}` → error: "task is <status>; reverse explicitly via `endless task update --status`."
4. If status is NOT in `{assumed, confirmed, completed}` (i.e., already non-terminal) → error: "task is <status>; reopen is only valid from a terminal status."
5. Read `tasks.text`. If non-empty → flip status to `ready`. If empty → flip status to `needs_plan`.
6. Emit the status-change audit event.
7. Print: `Reopened E-NNNN: status <prev> → <new>` (and `text: present | absent` to make the decision visible).
8. Exit 0.

## `endless task spawn <id> --reopen` behavior

1. Validate the task id exists.
2. Read current status.
3. If status is in `{ready, needs_plan, in_progress, revisit, blocked}` (already non-terminal) → error: `--reopen passed but task is <status>, not terminal`. (Enforce explicit intent; do not silently no-op.)
4. If status is in `{declined, obsolete}` → same error as the `reopen` verb (steer to `task update --status`).
5. If status is in `{assumed, confirmed, completed}`:
   - Apply the reopen semantics above (flip status; audit event).
   - Then proceed with the existing `task spawn` flow against the now-non-terminal task. The spawned session gets bound; the orchestrating session does not.

## `endless task spawn <id>` (no `--reopen`) on a terminal target

Today: probably errors with "task is `<terminal-status>`, cannot spawn" or similar. Update the error message to be actionable: `task is <terminal-status>; pass --reopen to reopen-and-spawn, or run 'endless task reopen <id>' first.`

## Out of scope

- Reversing `declined` or `obsolete` (use `task update --status --reason`).
- Worktree provisioning or teardown (reopen is metadata-only).
- Any change to `claim --force` semantics (still the right verb for self-pickup).
- Any change to existing `spawn` behavior when invoked on a non-terminal target without `--reopen`.

## Schema referenced

No new tables or columns. Uses existing `tasks` (status field) and whatever existing event channel handles status-change audit.

## Design notes

- **Why not auto-reopen on spawn (no flag required)?** Mike's call: "would allow tasks to be accidentally reopened." Requiring `--reopen` makes the intent explicit; the error message on the no-flag path points the user at the flag.
- **Why two affordances instead of just `--reopen` on spawn?** The standalone verb lets an orchestrating agent reopen a task without committing to who picks it up (could be spawn, could be self-pickup with a follow-up `claim`, could be a hand-back to Mike to decide).
- **Status target heuristic**: ready vs needs_plan based on `tasks.text` presence matches how `task update --text` auto-promotes `needs_plan` → `ready`. Reopen uses the same signal.
- **Audit visibility**: the reopen status-change should be discoverable in the event log so retrospective queries can see "this task was reopened on day X by session Y."

## Verification

1. `task reopen <id>` on `assumed` task with text → status flips to `ready`; audit event emitted; no session binding side effect.
2. `task reopen <id>` on `confirmed` task without text → status flips to `needs_plan`.
3. `task reopen <id>` on `completed` (epic) task → status flips per text presence; the epic's children are unaffected.
4. `task reopen <id>` on `ready`/`in_progress`/etc. → error names current status and refuses.
5. `task reopen <id>` on `declined`/`obsolete` → error steers to `task update --status --reason`.
6. `task spawn <id> --reopen` on `assumed` task → task reopens; spawn proceeds; the spawned session is bound to the task; the operating session is NOT bound.
7. `task spawn <id> --reopen` on already-`ready` task → error: explicit intent enforced.
8. `task spawn <id>` (no flag) on `assumed` task → error message names `--reopen` as the fix.
9. Operating session bindings before and after `task reopen` are unchanged (no binding side effect).

## Status

Phase `later`; this is a small QoL fix for a real but workaroundable gap. Ready to plan-attach for Mike's review; status flips to `ready` on plan attach via `task update --text`. No blockers; can land independently of the E-1421 family.
