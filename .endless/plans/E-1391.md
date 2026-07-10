You are implementing **E-1391** — worktree lifecycle visibility. Three pieces in one slice: new event kinds, new materialized table, new read CLI. Implements E-1352 (events-as-primitive); follows E-1322's pattern (commit `4652b1a` on main).

## What this is

Today, `endless worktree create` / `endless worktree drop` (and the underlying `endless task claim` auto-create path) modify the filesystem but emit no DB-recorded events. There's no answer to "which worktrees does session N have, and which did it once have?" except by scanning the filesystem.

Three pieces:

1. **Two new event kinds**: `worktree.created`, `worktree.destroyed`. Emitted from the worktree create/drop paths in the endless CLI.
2. **New table** `session_worktrees(session_id, worktree_path, branch_name, created_at, destroyed_at NULL until destroy)`. UNIQUE(session_id, worktree_path). Auto-captured via handlers.
3. **New CLI** `endless session worktrees [--session N] [--active]` listing active and historical worktrees for a session.

## Schema (V10 migration in `internal/monitor/db.go`)

```sql
CREATE TABLE session_worktrees (
    id             INTEGER PRIMARY KEY,
    session_id     INTEGER NOT NULL,
    worktree_path  TEXT    NOT NULL,
    branch_name    TEXT    NOT NULL,
    created_at     TEXT    NOT NULL,
    destroyed_at   TEXT    NULL,
    UNIQUE(session_id, worktree_path)
);
CREATE INDEX idx_session_worktrees_active ON session_worktrees(session_id, destroyed_at) WHERE destroyed_at IS NULL;
```

**ALL tables get a surrogate `id INTEGER PRIMARY KEY` as the first column** — Mike's 35+-year DB rule, no exceptions. The UNIQUE(session_id, worktree_path) stays as a constraint but is NOT the primary key. E-1322's session_tasks landed without an id PK; that's being fixed in E-1396. Do not repeat the omission here.

NO FK on session_id (matches the rationale on session_tasks per `4652b1a`: a materialized index must outlive its referenced entities; the JSONL ledger is the audit source).

## Event kinds (`internal/events/event.go`)

Add two new Kind constants:

```go
KindWorktreeCreated   Kind = "worktree.created"
KindWorktreeDestroyed Kind = "worktree.destroyed"
```

Register both in `ValidKinds`. Payloads in `internal/events/payload.go`:

```go
type WorktreeCreatedPayload struct {
    Path        string `json:"path"`
    BranchName  string `json:"branch_name"`
    TaskID      int64  `json:"task_id,omitempty"`
}
type WorktreeDestroyedPayload struct {
    Path string `json:"path"`
}
```

## Handlers (`internal/events/executor.go`)

Two new exec funcs: `execWorktreeCreated`, `execWorktreeDestroyed`. Wire to dispatch switch. Both check `shouldRecordSessionTouch(evt)` — **use the same helper that E-1381 fixed; the guard is `evt.Actor.SessionID != ""`, NOT `Kind == ActorSession`**. If E-1381 isn't merged yet, do the fix as part of this task — but verify it landed separately first.

execWorktreeCreated: INSERT into session_worktrees (session_id, worktree_path, branch_name, created_at).
execWorktreeDestroyed: UPDATE session_worktrees SET destroyed_at = now() WHERE session_id = ? AND worktree_path = ? AND destroyed_at IS NULL.

## Emit points (Python side)

Find where `endless worktree create` / `endless worktree drop` are implemented (probably `src/endless/worktree_cmd.py` or similar). After successful create/destroy, emit the corresponding event via the existing `event_bridge.emit_event` path.

Also: `endless task claim` and `endless task spawn` auto-create worktrees per CLAUDE.md. Trace where the worktree-add actually happens; emit `worktree.created` there too. ONE emit point per actual creation, not per CLI verb.

## CLI surface

```
endless session worktrees [OPTIONS]

Options:
  --session N    Session id (default: current pane's session)
  --active       Only worktrees that haven't been destroyed
  --format [table|json]
```

Output: branch / path / created_at / destroyed_at (or "active").

Same Go-side read pattern as E-1390 (monitor function + Python thin wrapper).

## Files to touch

- `internal/monitor/db.go` — add migrateV10 + bump CurrentSchemaVersion to 10
- `internal/events/event.go` — new Kind constants, ValidKinds entries
- `internal/events/payload.go` — payload structs
- `internal/events/executor.go` — new exec funcs + dispatch case
- `internal/events/worktree_test.go` (new) — handler tests
- `internal/monitor/session_worktrees.go` (new) — read helper
- `src/endless/worktree_cmd.py` — emit on create/drop
- `src/endless/task_cmd.py` or wherever `endless task claim` auto-creates — emit on auto-create
- `src/endless/session_cmd.py` — new subcommand for `session worktrees`

## Critical: actor guard

Per LESSONS.md "Verify the actor model before specifying actor-based guards" — the guard is `evt.Actor.SessionID != ""`. Do NOT gate on `Kind == ActorSession`. CLI invocations carry `Kind=cli` with `SessionID` populated; that's the path we want to record. If you're not sure, grep `.endless/db-ledger/*.jsonl` for actor.kind distribution — it's all `cli` in practice.

## E2E verification (critical)

```
# Create a worktree via real CLI
XDG_CONFIG_HOME=$HOME/.config endless worktree create test-probe-1391
XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_worktrees WHERE worktree_path LIKE '%test-probe-1391%'"
# Should show one row, destroyed_at NULL

# Drop it
XDG_CONFIG_HOME=$HOME/.config endless worktree drop test-probe-1391
XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT * FROM session_worktrees WHERE worktree_path LIKE '%test-probe-1391%'"
# destroyed_at populated

XDG_CONFIG_HOME=$HOME/.config endless session worktrees                  # historical row shows up
XDG_CONFIG_HOME=$HOME/.config endless session worktrees --active         # no row (it's destroyed)
```

## House rules

- `just build`, not bare `go build`.
- ClearPath style, doterr error wrapping, go-dt for paths (worktree_path is a path — use go-dt's DirPath type if available).
- Use the in-tx dbQuerier pattern from E-1322's commit `4652b1a` (no fresh-connection acquires inside handlers; SQLite SetMaxOpenConns(1) deadlocks otherwise).

## Closing

- `endless task update E-1391 --status verify --outcome "worktree.* events emit on create/drop; session_worktrees materialized; CLI lists active and historical; e2e verified."`
- Handoff: "To verify: `endless worktree create x`, then `endless session worktrees --active` shows it; `endless worktree drop x` flips destroyed_at."
- Do NOT auto-land. Ask Mike.
