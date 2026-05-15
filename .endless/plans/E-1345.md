# E-1345: Apply schema.sql as V0 baseline before Go migration framework

## Context

Fresh sandbox DBs created via `endless task spawn` (and any new install path that doesn't run the retired E-1118 Python stopgap) are missing the foundational tables `projects`, `tasks`, `notes`, `activity`, and `project_deps`. The Go migration framework in `internal/monitor/migrate.go` starts at V1, but `migrateV1` only *patches* a pre-existing baseline (ALTER TABLE, IF NOT EXISTS column adds, renames). The V0 baseline tables were historically created by the Python stopgap; with that gone, new DBs end up with the patch-set tables (task_deps, channels, conversations, messages, task_files, suggestions, session_*) but no baseline.

Symptom: spawning E-1327 produced a non-blocking hook error:
```
auto-registering project e-1327 at <path>: SQL logic error: no such table: projects (1)
```

The canonical V0 baseline schema is already in source at `internal/schema/schema.sql` and is already embedded as `schema.SQL` via `internal/schema/schema.go` (which uses `//go:embed`). It's already executed against a fresh DB by `internal/events/projector.go:51`, so the precedent for "run schema.SQL on an empty DB" exists.

Goal: execute `schema.SQL` as a V0 bootstrap inside `migrate()`, before the version-gate logic, so every DB has the baseline tables before V1-V8 patches run.

## Approach

**Single change point**: At the top of `migrate()` in `internal/monitor/migrate.go`, after `ensureSchemaVersionTable(db)` but before `userVersion(db)`, execute `schema.SQL` via `db.Exec(...)`. The schema file is fully idempotent (every table uses `CREATE TABLE IF NOT EXISTS`; every index/trigger uses `IF NOT EXISTS`), so re-running it on an already-populated DB is a no-op.

**Why at the top of `migrate()` and not in `DB()`**: `migrate()` is the single entry point used by both auto-migrate (`monitor.DB()` at db.go:72) and explicit CLI calls (`endless db migrate`). Bootstrapping inside `migrate()` covers both paths and keeps schema bootstrap colocated with version tracking.

**Why not register V0 in `_schema_version`**: The audit table is for *versioned migrations*, not for bootstrap. The V1+ rows continue to indicate "this DB has been through these migrations" — adding a V0 row would be cosmetic and would complicate the existing post-V4 fast-path. The fast-path at line 233 (`cur == 0 && hasTable(db, "suggestions")`) already does the right thing once `suggestions` exists from schema.SQL: it backfills V1-V4 audit rows without re-running V1-V4 bodies, then runs V6+.

**Self-healing**: Any existing busted sandbox DB (the ones already on disk like `worktree-e-1281`, the just-exited `worktree-e-1327`) will be repaired on its next open, because schema.SQL is idempotent CREATE IF NOT EXISTS.

**No effect on healthy DBs**: `~/.config/endless/endless.db` and any sandbox that was correctly bootstrapped will run schema.SQL as a series of no-ops. Foreign-key references inside schema.SQL (e.g., `sessions.active_task_id REFERENCES tasks(id)`) work fine at CREATE-TABLE time even when target tables don't yet exist — SQLite only enforces FKs during DML.

## Files modified

### `internal/monitor/migrate.go`
- Add import: `"github.com/mikeschinkel/endless/internal/schema"`
- In `migrate()`, after the `ensureSchemaVersionTable(db)` call (around line 201), add:
  ```go
  _, err = db.Exec(schema.SQL)
  if err != nil {
      err = doterr.NewErr(ErrMigrationFailed, doterr.StringKV("phase", "baseline"), err)
      goto end
  }
  ```

### `internal/monitor/baseline_test.go` (new)
A test using a file-backed temp SQLite DB (modernc.org/sqlite is the project's driver — see `events/projector.go`):
- `TestMigrateFreshDB_CreatesBaselineTables`: open empty DB, call `migrate()`, assert every table in schema.SQL exists (projects, project_deps, notes, sessions, tasks, task_deps, activity, channels, conversations, messages, session_messages, task_files, suggestions) plus the V6-V8 additions (session_gates, session_statuses).
- `TestMigrateIdempotent`: call `migrate()` twice on the same DB, assert no errors and `_schema_version` has no duplicate version rows.

## Verification

End-to-end after implementation:

1. `just build` to compile.
2. Run package tests: `go test ./internal/monitor/...`
3. Provision a fresh test sandbox and confirm tables are present:
   ```sh
   bin/endless-sandbox init test-baseline-e1345
   XDG_CONFIG_HOME=~/.cache/endless/sandboxes/test-baseline-e1345/endless \
     bin/endless task show >/dev/null 2>&1 || true
   sqlite3 ~/.cache/endless/sandboxes/test-baseline-e1345/endless/endless.db \
     "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;"
   ```
   Expect `projects`, `tasks`, `notes`, `activity`, `project_deps` present.
4. Cleanup: `bin/endless-sandbox destroy test-baseline-e1345`
5. Sanity-check the main DB isn't disturbed: `sqlite3 ~/.config/endless/endless.db "SELECT * FROM _schema_version ORDER BY version;"` should be unchanged (still 8 rows, V1-V8, no new V0 row).
6. After Mike re-spawns E-1327, confirm `~/.cache/endless/sandboxes/worktree-e-1327/endless/log/hook.log` has no "no such table" errors and `endless task detail E-1327` works inside the worktree.

## Critical files referenced

- `internal/monitor/migrate.go` — the only Go file edited
- `internal/monitor/migrate.go:188-228` — `migrate()` body where insert goes
- `internal/monitor/migrate.go:233` — the `hasTable(db, "suggestions")` fast-path that naturally handles fresh-DB-after-schema.SQL
- `internal/monitor/db.go:46-79` — `DB()` entry point, calls `migrate()` automatically
- `internal/schema/schema.sql` — V0 baseline (217 lines, fully `IF NOT EXISTS`)
- `internal/schema/schema.go` — embeds schema.sql as `schema.SQL`
- `internal/events/projector.go:28,51` — existing precedent for `db.Exec(schema.SQL)` on a fresh DB

## Out of scope

- Proactive repair of existing busted sandboxes (e.g., `worktree-e-1281`). Self-healing on next migrate() covers it; no sweep needed.
- The vestigial `focuses` table in the main DB (present in `~/.config/endless/endless.db` but not referenced anywhere in code and not in schema.sql). Leaving it alone.
- The `_schema_version` audit row scheme. Not changing.
