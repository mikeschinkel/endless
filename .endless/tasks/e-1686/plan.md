# E-1686 — Revive a session stuck in 'ended' when its own activity continues

## Context

Once a `sessions` row is marked `ended` — by a child `/exit` (`EndSession`), the
pane reaper (`ReapDeadTmuxPanes`), or collision invalidation inside `TouchSession`
— the same session's continued hook activity never lifts it back to a live state.
`TouchSession` is the per-event heartbeat that fires on *every* hook, but its
`ON CONFLICT(session_id) DO UPDATE` branch (`internal/monitor/session.go:294-301`)
deliberately bumps only `last_activity` and `process`, never `state`
(documented at `session.go:259`, `session.go:292-293`). That rule correctly avoids
clobbering `working`↔`idle`, but it also means an `ended` row has **no recovery
path** even though incoming hooks are proof the session is alive.

Every reader filters `state != 'ended'` (load-bearing for E-1530: tmux reuses `%N`
pane ids after a server restart, so ended rows must stay excluded) — confirmed at
`session_focus.go:38`, `reap.go:55/71`, `session_next.go:73/195`,
`live_sessions.go:45`, and `tmux_lookup.go`. So a live session stuck `ended` goes
invisible: blank tmux status line, `session next`/nav find nothing, and `task bind`
silently binds onto the dead row (`execTaskClaimed` updates `active_task_id` but
not `state`).

**Fix:** when a hook arrives for a session whose row is `ended`, revive it to a live
state. Gate the revival on the **`session_id` match** (the `ON CONFLICT(session_id)`
target already does this), NOT a bare pane match — so a reused `%N` belonging to a
*different* session_id still takes the INSERT path and the old ended row stays
ended, preserving E-1530.

## Reproduction (failing test, to add first)

Deterministic from the SQL — `TouchSession`'s UPDATE never lists `state`:

1. `TouchSession("sess-A", "claude", "%5", 1)` → row created `state='needs_input'`.
2. Force `UPDATE sessions SET state='ended' WHERE session_id='sess-A'` (simulates a
   reaper/collision/child-`/exit` ending).
3. `TouchSession("sess-A", "claude", "%5", 1)` again (the session's continued activity).
4. **Current (bug):** `state` is still `ended`. **After fix:** `state='needs_input'`.

Live evidence from the analysis: main DB session 822 (`process='%247'`,
`state='ended'`, current `last_activity`, `active_task_id=1540`) — ended once
(process NULLed), then its own later hooks re-populated `process` via the COALESCE
while `state` stayed `ended`.

## Decisions (confirmed with Mike)

- **Revive target state: `needs_input`** — mirrors `TouchSession`'s own INSERT
  default so a revived row is indistinguishable from a freshly-touched one; the next
  lifecycle hook (`Stop`→idle, prompt-flow→working, `SessionEnd`→ended) corrects it.
- **Also revive in `execTaskClaimed`** — make `task bind` self-sufficient regardless
  of hook timing, not just reliant on the TouchSession path.

## Changes

### 1. `internal/monitor/session.go` — `TouchSession` UPDATE branch (primary fix)

Add a `state` column to the `ON CONFLICT(session_id) DO UPDATE SET`, using a CASE so
ONLY an `ended` row is lifted; every live state is left exactly as-is (keeps
`working`↔`idle` authoritative — `TestTouchSession_StatePreservedAcrossUpdate` must
still pass):

```sql
ON CONFLICT(session_id) DO UPDATE SET
  last_activity = excluded.last_activity,
  process       = COALESCE(NULLIF(excluded.process, ''), sessions.process),
  state         = CASE WHEN sessions.state = 'ended' THEN 'needs_input' ELSE sessions.state END
```

Update the doc comment at `session.go:253-260` and the inline comment at
`session.go:289-293`: TouchSession no longer "deliberately does not change state on
UPDATE" — it now revives `ended`→`needs_input` (activity is proof-of-life), while
still never clobbering a live state. Note the E-1530 gating (session_id conflict
target, not pane match). The collision-invalidation block (`session.go:313-325`) is
unaffected — it runs after, targets *other* session_ids (`session_id != ?`), and
this row's revival doesn't interact with it.

### 2. `internal/events/executor.go` — `execTaskClaimed` (secondary facet)

In the `UPDATE sessions SET active_task_id = ..., active_epic_id = ...` statement
(starts `executor.go:812`), add the same CASE-based revive so binding a task to an
`ended` row also lifts it to a live state:

```sql
SET active_task_id = ?,
    state = CASE WHEN state = 'ended' THEN 'needs_input' ELSE state END,
    active_epic_id = ( ... existing subquery ... )
```

Add a short comment explaining the revive mirrors the TouchSession fix so `task bind`
can't land on (and silently report success against) an invisible dead row.

### 3. Tests

`internal/monitor/session_test.go` — add, modeled on the existing
`TestTouchSession_*` tests (use `withTestDB(t)` + `seedProject`, read via
`sessionRow`):

- `TestTouchSession_RevivesEndedRow` — the reproduction above: force `ended`, touch,
  assert `state='needs_input'`.
- `TestTouchSession_DoesNotReviveLiveStates` (or extend
  `TestTouncSession_StatePreservedAcrossUpdate`) — force `working`, touch, assert
  still `working`; same for `idle`. Guards the CASE so revival is `ended`-only.
- Optional E-1530 guard: a `TouchSession` for a *different* session_id on the same
  pane must NOT revive the original ended row (it takes the INSERT path); assert the
  ended row stays ended. (Reuse the shape of
  `TestTouchSession_CollisionInvalidationMarksPriorEnded`.)

`internal/events/` — if there's an existing `execTaskClaimed`/task.claimed executor
test, add a case binding to a pre-`ended` session row and asserting it becomes
`needs_input`. Otherwise cover via the lifecycle test harness if one exists; do not
invent a new harness solely for this.

### 4. `tests/tasks/e-1686-verify.sh` — end-to-end verify script

Per the per-task verify-script convention (E-1596 epic; model the shape/output on the
recent `tests/tasks/e-1685-verify.sh` — `set -u`, colored pass/fail + summary, exit 0
all-passed / 1 on any failure, seeds and reads against the worktree's **sandbox DB**).
Make it executable (`chmod +x`).

**Lever:** the revive is triggerable headlessly through the candidate Go binary —
`./bin/endless-go ensure-claude-id --session-id <uuid> --project-root "$repo_root"
--process <pane>` calls `EnsureClaudeSessionID → TouchSession` (`session_query.go:264`).
Seed/inspect the row directly with `sqlite3 "${SANDBOX_DB}"` (same `SANDBOX_DB` path
derivation as e-1685-verify.sh: `~/.cache/endless/sandboxes/$(basename repo_root)/endless/endless.db`).

Checks:
- **Revive on own activity (primary):** `ensure-claude-id` once to create the row;
  `UPDATE sessions SET state='ended'` via sqlite3 (simulates reaper/collision/child-exit);
  `ensure-claude-id` again with the SAME session_id + pane; assert `state='needs_input'`.
  Then assert the session is visible again to a `state != 'ended'` reader (e.g.
  `ListLiveSessions` via its CLI surface, or a direct `SELECT ... WHERE state != 'ended'`
  count for that session_id).
- **Live state not clobbered:** seed, `UPDATE ... SET state='working'`, `ensure-claude-id`,
  assert still `working` (guards the CASE is `ended`-only). Repeat for `idle`.
- **E-1530 reused-pane safety:** seed session A `ended` on pane `%X`; `ensure-claude-id`
  for a DIFFERENT session_id B on the same `%X`; assert A stays `ended` (B took the INSERT
  path; A is not revived) and B is live.
- **task bind facet (best-effort):** if a clean headless lever exists to emit a
  `task.claimed` for a specific seeded session row, seed it `ended`, bind, assert
  `needs_input`. If no clean lever, leave this facet to the Go executor test and note so
  in a script comment — don't contort the script.

## Verification

After `esu` then `just build` (stale Go binaries reject behavior changes), the agent's
verify handoff is simply:

```sh
esu
./tests/tasks/e-1686-verify.sh
```

Supporting checks (the script encapsulates the behavioral ones above):

- `go test ./internal/monitor/... ./internal/events/...` — new tests pass; pre-existing
  `TestTouchSession_StatePreservedAcrossUpdate`, `TestTouchSession_CollisionInvalidation*`,
  and the dedup tests still pass.
- `just test` (Python) stays green — no Python surface touched.

## Out of scope

- E-1468 (prevent the collision-invalidation false-ending via a liveness check) —
  complementary, separate task; both touch `session.go`, coordinate edits but don't
  fold in.
- Any reader-side `state != 'ended'` filter changes — the filter is correct and
  load-bearing; the fix is purely making `ended` recoverable at the write sites.
