# E-1640 — Fix duplicate session rows minted per Claude launch when TMUX_PANE is empty

**Independent (parent of E-1641).** Full evidence in this task's `analysis` field. · **Layer:** Go

## Root cause (recap)
Dedup key is `sessions.session_id` (the Claude UUID), not `active_task_id`. Every launch gets a
new UUID, so SessionStart's upsert finds no conflict and **INSERTs a new row** via `TouchSession`
(`internal/monitor/session.go:236-295`, from `internal/hookcmd/claude.go:161`) and `InitSession`
(`session.go:104-121`). The only intended dedup — collision-invalidation by `process` (tmux pane),
`session.go:280-292` — is **inert** because `TMUX_PANE` is empty: `process` is NULL on 728/744
rows and `COALESCE(NULLIF(excluded.process,''))` refuses to store an empty pane. Sub-10s ghosts accrue.

## Files
- `internal/monitor/session.go`: `TouchSession` (220-295), `InitSession` (104-121), collision
  invalidation (280-292), `BindSessionToTask` (38-60).
- `internal/hookcmd/claude.go`: SessionStart ordering (154-180), `TouchSession` call (~161).

## Approach — active_task_id-scoped fallback dedup for the empty-pane case
When `process` is empty **and** `active_task_id` is known, fold into the existing recent
non-ended row for that task instead of inserting a fresh-UUID row:
- **Task known at SessionStart?** For spawned sessions yes (`@endless_task_id` window var read by
  the hook). Confirm binding order: if `BindSessionToTask` runs *after* `TouchSession`, move/peek
  the task id earlier or run the fallback on the bind step.
- **Fold rule:** if a non-ended row exists for the same `active_task_id` with empty `process` and
  recent `last_activity`, reuse it (update `session_id` + `last_activity`) rather than INSERT.
  Scope tightly (same task + empty process + recent) so two genuinely concurrent sessions aren't merged.
- **Ad-hoc (unbound) sessions:** task unknown → still create a row (real session until bound).

## Verification — create a per-task verify script
Create `tests/tasks/e-1640-verify.sh` in the `e-1624-verify.sh` shape (bash, `set -u`, the
`section`/`report_*`/`summary` helpers, `cd` repo root, ensure `go.work`, exit 0/1/2). Drive the
REAL code path via Go tests in `internal/monitor` — don't re-implement dedup logic.

The script asserts (named checks):
- `internal/monitor` compiles (`go build ./internal/monitor/`).
- A Go test (`go test -count=1 -run ^TestTouchSession_FoldsEmptyPaneSameTask$ ./internal/monitor/`)
  drives repeated `TouchSession` with empty `process` + the same `active_task_id` and asserts the
  row count stays **1** (folds), not N.
- A second Go test asserts a genuinely separate, freshly-claimed session on the same task is **NOT**
  folded into the stale row.
- `internal/monitor` full suite stays green (`go test -count=1 ./internal/monitor/`).

Re-runnability (sandbox/test DB):
- Tests build their own schema-applied DB and derive ids freshly — never hardcode a UNIQUE
  `session_id`/`short_id`; use fresh per-run values.
- Run the script at least TWICE before declaring it done.

## Implemented (2026-06-29) — decisions confirmed with Mike

The original "fold/reuse the row (update `session_id`)" wording above was **not**
used. Two decisions changed it:

1. **Mechanism = end stale rows, not mutate `session_id`.** Mutating a surviving
   row's `session_id` is FK-unsafe in production: `session_messages.session_id`
   REFERENCES `sessions(session_id)` ON DELETE CASCADE only (no ON UPDATE), so
   the update throws `FOREIGN KEY constraint failed` whenever the fold target has
   any messages — and there is no migration path to add ON UPDATE CASCADE
   (schema.sql `CREATE TABLE IF NOT EXISTS` can't alter existing DBs; the
   V-migration framework was removed). Instead: keep the new-UUID row, and in
   `BindSessionToTask` end any OTHER non-ended foreground (`kind_id` = tmux) row
   for the same `active_task_id` that has no pane. FK-safe (only `state` changes),
   mirrors the existing collision-invalidation idiom.
2. **No recency window.** End all prior matching rows regardless of
   `last_activity` age. A resume can happen long after the prior row went idle, so
   a recency window would miss that duplicate; concurrency-merge is prevented by
   worktree locks (one live foreground session per task) plus the `kind_id` guard
   that excludes background agents.

**Code:** `internal/monitor/session.go` `BindSessionToTask` — bind upsert +
scoped invalidation now run in one transaction. All bind paths (spawn-marker
auto-bind, `autoBindFromCwd`, `StartWorkSession`) route through it.

**Tests:** `internal/monitor/session_dedup_test.go` —
`TestBindSessionToTask_EndsStalePanelessRowsSameTask`,
`_RepeatedLaunchesStayAtOneLiveRow`, `_DoesNotEndBackgroundAgentSameTask`,
`_DoesNotEndPanedRowSameTask`, `_DoesNotEndOtherTasksRows`. (Named for the actual
bind-step mechanism, not the plan's `TestTouchSession_Folds*`.)

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1640-verify.sh
```


