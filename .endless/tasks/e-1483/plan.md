# Context

Sessions last for days. `sessions.active_task_id` is the only session→task pointer today, and both `endless task claim` and `endless task bind` overwrite it. When a session pivots to a follow-up (a `cleans_up` child filed mid-stream, an in-session-discovered fix), the origin task is lost from the tmux status bar and from any cold-return orientation. Returning to a multi-day session and seeing `[E-NNNN]` in the status while the conversation is about a follow-up is exactly the failure mode this addresses.

Reading back through the conversation log as the recall mechanism is unworkable in practice — too slow, too exhausting. The status bar needs to carry both pieces of state.

# Implementation language — Go-only for DB access

**All DB access (reads AND writes) for this work happens in Go.** The Python side does only event emission and Click wiring. Do not add any `db.query(...)` or `db.execute(...)` against the `sessions` table from Python. This is the destination state E-1486 (umbrella for "Python has zero SQLite knowledge") is moving the project toward; E-1483 is blocked by E-1486 so the necessary Go-side infrastructure — session read helpers and projector write paths — is in place before E-1483 lands.

Concretely:

- **Writes**: the projector handler for `session.goal_set` (Go) is the only write path to `sessions.goal_task_id`. The BEFORE UPDATE trigger (also Go-side, since schema is owned by Go per E-1472) is the authoritative gate.
- **Reads** (e.g. the `bind --include-goal` pre-check that needs to know whether `goal_task_id` is currently NULL): call whichever Go read subcommand E-1486 ships for session lookup. Do not introduce a Python read path against `sessions` even temporarily. If the right Go subcommand does not yet exist when E-1483 is picked up, flag it as a missing E-1486 sub-deliverable rather than working around it in Python.

# Decisions

## Schema

- **New column**: `sessions.goal_task_id INTEGER`, nullable, foreign key to `tasks(id)` ON DELETE SET NULL.
- **Write-once invariant**: once `goal_task_id` is non-NULL for a session, it cannot be changed to a different non-NULL value. Idempotent re-set with the same value is allowed.
- **Enforcement via BEFORE UPDATE trigger** (decision E-1495):

  ```sql
  CREATE TRIGGER sessions_goal_task_id_write_once
  BEFORE UPDATE OF goal_task_id ON sessions
  WHEN OLD.goal_task_id IS NOT NULL
   AND NEW.goal_task_id IS NOT OLD.goal_task_id
  BEGIN
      SELECT RAISE(ABORT, 'goal_task_id is write-once; cannot change once set');
  END;
  ```

  Allows NULL → any (first write) and idempotent re-set (NEW IS OLD); refuses any change-after-set. CHECK constraints are NOT used — prohibited project-wide per E-892. Application-layer pre-checks (see `bind --include-goal` below) are additive for friendly error messages; the trigger is the authoritative gate.

- **The three existing `sessions` fields `active_goal_id`, `primary_focus_id`, `current_focus_id` are out of scope** for E-1483. They belong to a stalled task-set Mike intends to re-imagine and must not be touched.

## CLI semantics

- **`endless task claim <id>`** writes both `active_task_id` and (if currently NULL) `goal_task_id` to `<id>` for the resolved session.
- **`endless task bind <id>`** writes only `active_task_id` to `<id>`; `goal_task_id` untouched.
- **`endless task bind <id> --include-goal`** writes `active_task_id` always; writes `goal_task_id` only if it is currently NULL. If `goal_task_id` is already non-NULL, bind refuses with hard error (exit 1) and prints: `goal already set, leaving as E-NNNN` (substituting the existing goal's task id). The pre-check is for the user-facing message; the trigger remains the authoritative gate.
- **`endless task release [<id>]`** clears `active_task_id` only; `goal_task_id` is untouched. A released session retains its origin pointer.

## Events

- **New event kind: `session.goal_set`**. Entity = `session`, payload = `{ "goal_task_id": <task_id> }`. Emitted alongside `task.claimed` from `claim_item` (when goal was previously NULL) and from `bind_item` with `--include-goal` (when goal was previously NULL). Never emitted on plain `bind` or on a same-value idempotent claim.
- The existing `task.claimed` event payload is **not** modified; the goal-set side effect is carried by the new event kind so that `bind --include-goal` has the surface it needs without overloading task.claimed.
- **Projector handler** for `session.goal_set` runs `UPDATE sessions SET goal_task_id = ? WHERE id = ?`. The BEFORE UPDATE trigger enforces the invariant; the handler does not pre-check.

## tmux status-line rendering

The status renderer reads `sessions.goal_task_id` and `sessions.active_task_id` and branches:

- `goal_task_id IS NULL` OR `goal_task_id = active_task_id` → render `[E-NNNN]` (today's behavior, single value, no divergence).
- `goal_task_id != active_task_id` → render `[E-GGGG/E-AAAA]`.

Colors:

- **Active** (E-AAAA): same color the renderer currently uses for `active_task_id` (the existing "bright yellow"). Look up the exact escape from the existing renderer; do not invent a new one.
- **Goal** (E-GGGG, in the diverged form): the same color the tmux integration uses for **unselected window tabs**. Look up the existing escape from the tmux config writer or the renderer; do not invent. The intent is "background context that you can see but does not pull focus."

If either color escape cannot be located in current source, surface that as a question before implementing rather than guessing.

# Files to modify

- `internal/schema/schema.sql` — add `goal_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL` to `sessions`; add the BEFORE UPDATE trigger above.
- `internal/events/payload.go` — define `SessionGoalSetPayload { GoalTaskID int64 }`; add `events.KindSessionGoalSet` constant.
- `internal/events/projector.go` — add handler for `session.goal_set` that UPDATEs `sessions.goal_task_id`.
- `cmd/endless-event/main.go` / `internal/events/validate.go` — register the new event kind in the validator allow-list.
- `src/endless/task_cmd.py` — Python event emission and CLI wiring only; **no SQL** (per Implementation-language section above). `claim_item` emits `session.goal_set` alongside `task.claimed` when the session's `goal_task_id` is currently NULL (NULL state read via Go subcommand). `bind_item` accepts a new `include_goal` parameter; when True, reads current `goal_task_id` via Go subcommand. Non-NULL → refuse with the `goal already set, leaving as E-NNNN` message, emit nothing. NULL → emit `session.goal_set`. When False, no goal-set event.
- `src/endless/cli.py` — `task bind` Click command gains `--include-goal` flag, threads through to `bind_item`.
- The tmux status-line renderer (likely under `cmd/endless-tmux/` or `internal/tmux/` — locate during implementation) — add the diverged-display branch reading `goal_task_id`.
- Tests below.

# Tests

Python:
- Extend `tests/test_task_claim_worktree.py`: after `claim` on a session with NULL goal, both `active_task_id` and `goal_task_id` equal the task id; after a second `claim` on a different task in the same session, `active_task_id` advances but `goal_task_id` stays at the original.
- New `tests/test_task_bind_include_goal.py`:
  - `bind --include-goal` on a session with NULL goal: both columns set; `session.goal_set` event emitted.
  - `bind --include-goal` on a session with non-NULL goal: refuses with `goal already set, leaving as E-NNNN`; no event emitted; columns unchanged.
  - Plain `bind` on a session with NULL goal: active changes; goal stays NULL; no `session.goal_set` event.
- Extend `tests/test_task_release.py`: release clears `active_task_id`, leaves `goal_task_id` intact.

Go:
- Projector test for `session.goal_set`: handler runs the UPDATE; affected-rows reflected; trigger fires on an attempted second-write-with-different-value (test the trigger directly with a raw UPDATE, expect SQLite error).
- Status-line renderer test: same / diverged / NULL-goal cases all produce the expected output strings with the right ANSI escapes.

`just test` (Python) and `just test-go` (Go) both green before flipping to `verify`.

# Out of scope

- The existing `active_goal_id`, `primary_focus_id`, `current_focus_id` columns on `sessions` (stalled task-set; not to be touched).
- Backfilling `goal_task_id` for existing session rows. New sessions get it populated on first claim; old sessions stay NULL.
- Migration logic for shipped installs (endless has one user, no shipped product).
- Mutability of `goal_task_id` via any non-claim/bind path. The trigger covers it.

# Verification

End-to-end manual checks after implementation lands:

1. **Fresh-session claim**: spawn a new session, `endless task claim E-XXXX`. Expect tmux status `[E-XXXX]`; `sessions.goal_task_id = sessions.active_task_id = XXXX`.
2. **In-session pivot via bind**: in the session from (1), `endless task bind E-YYYY`. Expect status `[E-XXXX/E-YYYY]` (goal in unselected-tab gray, active in bright yellow); `goal_task_id` unchanged, `active_task_id = YYYY`.
3. **Late goal-set**: session started without claim (NULL goal), `endless task bind E-ZZZZ --include-goal`. Expect both columns = ZZZZ; status `[E-ZZZZ]`.
4. **Write-once refusal**: in the session from (1), `endless task bind E-WWWW --include-goal`. Expect refusal with `goal already set, leaving as E-XXXX`; columns unchanged.
5. **Release preserves goal**: in the session from (1), `endless task release`. Expect `active_task_id` cleared, `goal_task_id` still XXXX, status reverts to whatever the renderer shows for "no active task" (existing behavior).
6. **Trigger is authoritative**: from `endless sql --write`, attempt `UPDATE sessions SET goal_task_id = 9999 WHERE id = <session>` on a session whose goal is set. Expect SQLite error from the trigger; row unchanged. Validates that the gate works even if app-layer is bypassed.

# Closeout

- Set status to `verify` when implementation is done and all tests pass. Include the six verification checks in the handoff so Mike can run them as part of confirming.
- Do not auto-land. Ask Mike.
