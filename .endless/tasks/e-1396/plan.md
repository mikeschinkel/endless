You are implementing **E-1396** — add a surrogate `id INTEGER PRIMARY KEY` column to the existing `session_tasks` table. E-1322 landed without one; the commit message defended the omission, but the omission violates a non-negotiable house rule. All tables get an id PK. No exceptions.

## What this is

`session_tasks` exists today (from E-1322, commit `4652b1a` on main) with this schema:

```sql
CREATE TABLE session_tasks (
    session_id INTEGER NOT NULL,
    task_id    INTEGER NOT NULL,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE(session_id, task_id)
);
```

The rule (LESSONS.md entry "Every table gets a surrogate `id INTEGER PRIMARY KEY`") requires `id` as the first column. UNIQUE compound stays as a constraint, NOT as the primary key.

## The fix

SQLite can't `ALTER TABLE ADD COLUMN` with a `PRIMARY KEY` clause. The migration is the standard SQLite table-rebuild dance:

```sql
CREATE TABLE session_tasks_new (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id    INTEGER NOT NULL,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE(session_id, task_id)
);

INSERT INTO session_tasks_new (session_id, task_id, created_at, updated_at)
SELECT session_id, task_id, created_at, updated_at FROM session_tasks
ORDER BY created_at;  -- preserves insertion order in the new surrogate ids

DROP TABLE session_tasks;
ALTER TABLE session_tasks_new RENAME TO session_tasks;

CREATE INDEX idx_session_tasks_task ON session_tasks(task_id);
```

ORDER BY created_at in the INSERT-SELECT matters: SQLite assigns `id` in insertion order, so older touches get smaller ids. Without the ORDER BY the order is engine-defined.

## Files to touch

- `internal/monitor/db.go` — add `migrateV10` (or whichever version is next free; check `migrations` slice). Bump `CurrentSchemaVersion`.
- `internal/events/session_tasks_test.go` — the test helper `newSessionTasksTestDB` duplicates the table create. Update it to match the new schema (id PK + UNIQUE). Run the existing tests; they should still pass since they don't depend on column-order or absence of id.
- A house-rule note. The natural home: a new section in `~/Projects/endless/docs/` or a Go skill. Or extend `CLAUDE.md` (the project one in the repo). The rule: "Every CREATE TABLE in this project gets `id INTEGER PRIMARY KEY` as the first column; UNIQUE constraints go AFTER. No exceptions." Document it where future schema authors are likely to look.

## Sequencing relative to other in-flight schema work

E-1391 (session_worktrees) and E-1392 (session_landings) prompts already include `id INTEGER PRIMARY KEY` in their new tables — they won't repeat E-1322's omission. E-1396 is purely a retrofit for the existing session_tasks. If E-1391 / E-1392 land first, they will claim consecutive migration versions; check the latest CurrentSchemaVersion when you start and use the next free.

## Verification

1. `just build` cleanly.
2. `just test` passes (session_tasks_test.go still green).
3. End-to-end against the real DB:
   ```
   XDG_CONFIG_HOME=$HOME/.config endless sql "PRAGMA table_info(session_tasks)"
   # Should show id as the first column, INTEGER, pk=1
   XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT id, session_id, task_id FROM session_tasks ORDER BY id LIMIT 5"
   # Should return rows with non-null ids; older touches have smaller ids
   XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT count(*) FROM session_tasks"
   # Same count as before the migration
   ```
4. Negative path: try `INSERT INTO session_tasks (session_id, task_id, created_at, updated_at) VALUES (451, 1390, 'now', 'now')` — should fail with UNIQUE constraint (since the backfilled row already exists).

## House rules

- `just build`, never bare `go build`.
- ClearPath / doterr / go-dt as applicable.
- The migration runs auto via `monitor.DB()` on next binary restart. No manual `endless db migrate` needed unless you want to force it.
- Migrations CURRENTLY swallow `db.Exec` errors (V3/V4/V8 pattern); E-1380 will fix that hygiene gap separately. For this migration, follow the existing pattern but also capture the table-rebuild step's errors locally for diagnostic clarity — the rebuild is risky enough (data loss if INSERT-SELECT fails before DROP) that the implementer should error-check explicitly even though the rest of the function doesn't.

## Closing

- `endless task update E-1396 --status verify --outcome "session_tasks rebuilt with id INTEGER PRIMARY KEY; data preserved; UNIQUE constraint intact; house-rule documented for future schemas."` Status is `verify`, not `completed` — Mike confirms.
- Handoff: "To verify: `endless sql 'PRAGMA table_info(session_tasks)'` shows id as pk; `SELECT id FROM session_tasks LIMIT 1` returns a non-null id."
- Do NOT auto-land. Ask Mike.
