# E-1322: session_tasks table + auto-upsert handler hooks

## Context

E-1352 established events-as-primitive — JSONL ledger is the source of truth, SQL tables are materialized projections. E-1285 confirmed read-time projection ("`endless session activity` reads the ledger") works for one direction. E-1322 lands the first **write-time materialized** per-kind table: `session_tasks`, recording "session N touched task M" so cross-session queries like "which sessions worked on task X" become simple-SQL fast.

Existing infrastructure assumed (verified):
- Events fire with `actor.session_id` already populated (E-1284).
- `dbQuerier` interface in `internal/events/executor.go:13-19` is satisfied by `*sql.Tx`; per E-1315, executors must never re-acquire connections — SQLite has `SetMaxOpenConns(1)` and nested acquires deadlock.
- `Actor.SessionID` (event.go:49) is a `string` holding the **numeric sessions.id encoded as text** (e.g. `"356"`) — same convention session_statuses uses (db.go:192, 203).
- `PRAGMA foreign_keys=ON` is set globally (db.go:69).

## Decisions (locked in)

1. **No FK constraints on session_tasks.** It's a materialized index; rows outlive their referenced session/task. The events JSONL is the audit source of truth. Required so `execTaskDeleted` and `execTaskBulkCleared` can record touches without the same-tx delete cascading them away.
2. **execTaskDeleted with `Cascade=true` records only `evt.Entity.ID`**, not the recursive child IDs. The session intended to touch the primary entity; cascaded children are derived effects.
3. **Actor guard is strict per spec:** upsert only when `evt.Actor.Kind == ActorSession && evt.Actor.SessionID != ""`. CLI/hook actors with a session_id set are not recorded (Mike's call — "Don't bind hook/cli/system actors as 'sessions'").

## Schema (V9)

```sql
CREATE TABLE session_tasks (
    session_id INTEGER NOT NULL,
    task_id    INTEGER NOT NULL,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE(session_id, task_id)
);

CREATE INDEX idx_session_tasks_task ON session_tasks(task_id);
```

- No `id` PK — natural key is `(session_id, task_id)`.
- UNIQUE serves "tasks touched by session N" lookups via the implicit index.
- Explicit index on `task_id` serves "sessions that touched task M".
- No FK refs (decision 1 above).

## Files to modify

### 1. `internal/monitor/db.go` — add `migrateV9` (after line 296)

Pattern modeled on V7 (db.go:188-219). Idempotent: `if !hasTable(db, "session_tasks") { CREATE ... }`. Then unconditional `CREATE INDEX IF NOT EXISTS idx_session_tasks_task ON session_tasks(task_id)` at the tail.

### 2. `internal/monitor/migrate.go` — register V9 + bump constant

- Line 15: `CurrentSchemaVersion = 8` → `9`.
- Line 53 (after V8 entry): append `{Version: 9, Name: "session_tasks (E-1322)", Apply: migrateV9},`.

### 3. `internal/events/session_tasks.go` — NEW

Helper function:

```go
package events

import (
    "strconv"
)

// upsertSessionTask records that the given session touched the given task.
// Called from each task.* executor when the event's actor is a session.
// Inserts a new row or bumps updated_at on conflict.
//
// Safe to call inside the in-flight tx via the dbQuerier passed by the
// dispatcher — per E-1315, executors must NOT acquire fresh connections.
func upsertSessionTask(db dbQuerier, sessionIDStr string, taskID int64) error {
    sessionID, err := strconv.ParseInt(sessionIDStr, 10, 64)
    if err != nil {
        return nil // malformed session_id — skip silently, not our concern
    }
    n := now()
    _, err = db.Exec(
        `INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
         VALUES (?, ?, ?, ?)
         ON CONFLICT(session_id, task_id) DO UPDATE SET updated_at = excluded.updated_at`,
        sessionID, taskID, n, n,
    )
    return err
}

// shouldRecordSessionTouch reports whether an event should produce a
// session_tasks row. Strict per E-1322 spec: ActorSession only, even
// though CLI actors may carry a session_id.
func shouldRecordSessionTouch(evt *Event) bool {
    return evt.Actor.Kind == ActorSession && evt.Actor.SessionID != ""
}
```

### 4. `internal/events/executor.go` — wire the upsert into each task.* handler

At the success tail of each handler (just before `return &ExecuteResult{...}, nil`), add:

```go
if shouldRecordSessionTouch(evt) {
    if err := upsertSessionTask(db, evt.Actor.SessionID, taskID); err != nil {
        return nil, fmt.Errorf("events: record session_tasks: %w", err)
    }
}
```

Per-handler `taskID` source:

| Handler | Line | taskID source |
|---|---|---|
| `execTaskCreated` | 138 | `taskID` local (already `int64`, line 172) |
| `execTaskImported` | 198 | `taskID` local (already `int64`, line 212) |
| `execTaskStatusChanged` | 228 | `mustParseInt64(evt.Entity.ID)` |
| `execTaskFieldsUpdated` | 278 | `mustParseInt64(evt.Entity.ID)` |
| `execTaskMoved` | 368 | `mustParseInt64(evt.Entity.ID)` |
| `execTaskDeleted` | 402 | `mustParseInt64(evt.Entity.ID)` — primary entity only, even on cascade |
| `execTaskBulkCleared` | 431 | special — see below |
| `execTaskClaimed` | 457 | `mustParseInt64(evt.Entity.ID)` |
| `execTaskReleased` | 472 | `mustParseInt64(evt.Entity.ID)` |

**`execTaskBulkCleared` (special):** No single primary entity — operation targets `(project_id, source_file)` and deletes N tasks. Per "probably yes per-cleared-task":

```go
// Before the existing UPDATE/DELETE:
if shouldRecordSessionTouch(evt) {
    rows, err := db.Query(
        "SELECT id FROM tasks WHERE project_id = ? AND source_file = ?",
        projectID, p.SourceFile,
    )
    if err != nil {
        return nil, fmt.Errorf("events: enumerate bulk_cleared tasks: %w", err)
    }
    var ids []int64
    for rows.Next() {
        var id int64
        if err := rows.Scan(&id); err != nil {
            rows.Close()
            return nil, fmt.Errorf("events: scan bulk_cleared id: %w", err)
        }
        ids = append(ids, id)
    }
    rows.Close()
    for _, id := range ids {
        if err := upsertSessionTask(db, evt.Actor.SessionID, id); err != nil {
            return nil, fmt.Errorf("events: record bulk_cleared touch: %w", err)
        }
    }
}
// ... existing UPDATE parent_id NULL + DELETE FROM tasks
```

The decoupling from FK constraints (decision 1) means these rows survive the subsequent DELETE.

### 5. `internal/events/session_tasks_test.go` — NEW

Coverage matrix (all using synthetic events fed through `Execute`):

1. `task.created` with ActorSession + SessionID="42" → exactly one row, created_at == updated_at.
2. `task.fields_updated` for same (42, taskID) → still one row, updated_at advances (compare strings), created_at unchanged.
3. `task.created` with ActorKind=ActorCLI + SessionID="42" → zero rows (strict guard).
4. `task.created` with ActorSession + SessionID="" → zero rows.
5. Two different sessions (42, 43) touch same task → two rows.
6. One session touches two tasks → two rows.
7. Same event applied twice (idempotency replay) → still one row, no error.
8. `task.deleted` (non-cascade) records a row even though the task no longer exists (verifies no-FK decision).

**Test scaffold:** No DB-integration test exists yet under `internal/events/` (verified — `commit_test.go` uses git tmp dirs, others test marshaling/rendering). Will need a minimal helper that:
- Opens a fresh `:memory:` SQLite DB OR points `monitor.dbConn` at a tmpfile.
- Runs migrations 1-9 to set up schema.
- Seeds a minimal `sessions` row (id=42) and `projects` row.
- Then fires events via `Execute`.

Simplest path: use `t.TempDir()` + override `XDG_CONFIG_HOME` so `monitor.DB()` opens a per-test database; run `Migrate(MigrateOpts{Runner: RunnerExplicit, SkipBackup: true})`; seed; test. The events package already imports `monitor`. If `monitor.DB()` is process-singleton-cached, may need to expose a reset hook — investigate during implementation. Fallback: build the schema directly with `db.Exec(CREATE...)` for the minimum tables needed (`sessions`, `projects`, `tasks`, `session_tasks`).

## Verification (end-to-end)

From inside this worktree (sandboxed DB):

```sh
just build && just test
# expect: green; new session_tasks tests pass

# manual smoke:
endless task add "session_tasks probe"
endless sql "SELECT * FROM session_tasks"
# expect: one row, session_id=<your sid>, created_at==updated_at

endless task update <probe-id> --description "x"
endless sql "SELECT * FROM session_tasks WHERE task_id=<probe-id>"
# expect: same row, updated_at > created_at

endless task claim <probe-id>
endless task release <probe-id>
endless sql "SELECT * FROM session_tasks WHERE task_id=<probe-id>"
# expect: still one row, updated_at advanced again

# negative path: a CLI-actor event (run outside Claude session)
XDG_CONFIG_HOME=/tmp/no-sid endless task add "cli-only probe"
# expect: no new session_tasks row for that task (Actor.SessionID empty)
```

Spot-check the migration:
```sh
endless sql "SELECT version, name, applied_at FROM _schema_version ORDER BY version DESC LIMIT 3"
# expect: V9 'session_tasks (E-1322)' row present
endless sql "PRAGMA table_info(session_tasks)"
# expect: 4 cols, no FK list
endless sql "PRAGMA index_list(session_tasks)"
# expect: one UNIQUE auto-index + idx_session_tasks_task
```

## Closing

- `endless task update E-1322 --status verify --outcome "session_tasks materialized table + auto-upsert hooks wired into all task.* exec handlers; FK-less per design (events JSONL is source of truth); verified by unit tests + synthetic CLI sequence."` — NOT completed.
- Handoff message: "To verify: `just build && just test`, then run the synthetic-event sequence in the plan's Verification section."
- Do NOT auto-land; ask Mike before `endless worktree land`.
- Commit refs E-1322 (and E-1352 in body for context).
