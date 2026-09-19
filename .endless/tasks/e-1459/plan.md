# E-1459: Replace migrate.go framework with schema.sql + per-ticket scripts applied at land

## Context

The `internal/monitor/migrate.go` framework was originally designed for the multi-user production case: rolling upgrades across an install base where DB-side state must be preserved across binary versions. Endless is currently pre-release with one user; that design payoff doesn't exist yet, but its cost does:

- Sequential `V<N>` numbering forces parallel branches to fight for "the next number" (E-1434 collided with E-1396's V11 mid-session and required manual rebase resolution to land).
- Each schema change costs a `migrateV<N>` function + registry append + version bump + audit comment, even for trivially-additive tables.
- Multiple bugs in the framework are filed but unfixed (E-1380, E-1453, E-1454) — code we'd delete instead of patching.

Replace the framework with a simpler model: schema.sql as source of truth (read by sqlc for queries), one-off destructive changes as per-ticket scripts applied at `just land`, `_schema_version` reused as the applied-marker. No version constants, no `migrateV<N>` functions, no append-only registry race.

## Design

### Source of truth: schema.sql

`internal/schema/schema.sql` is the authoritative schema. All `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`. Read on every DB connection (already is via `db.Exec(schema.SQL)` in `DB()`). Additive changes — new tables, new nullable columns, new indexes — go here directly.

sqlc reads `schema.sql` to generate query-row types. Adopt sqlc as part of this task; it was deferred earlier.

### Destructive changes: per-ticket files

One change per task in `internal/schema/changes/`, named `e-NNN-short-slug` (matching the existing branch convention `task/NNN-short-slug`). Examples:
- `e-1404-autoincrement-primary-keys.sql`
- `e-1459-reshape-schema-version.sql`
- `e-NNN-backfill-normalized-paths.go`

Two file types are first-class:

- **`.sql`** — pure SQL changes. Openable in a SQL client (syntax highlight, dry-run against a backup pre-land). Default choice when the change expresses cleanly as SQL.
- **`.go`** — when the change needs conditional logic, intermediate query results, or parsing helpers from internal packages. The author's file is a `package main` program that imports the tiny runner helper and calls `runner.Run` with the work as a callback:
  ```go
  package main

  import (
      "database/sql"
      "github.com/mikeschinkel/endless/internal/schema/changes/runner"
  )

  func main() {
      runner.Run(func(tx *sql.Tx) error {
          // do work using tx
          return nil
      })
  }
  ```
  `runner.Run` opens the DB, derives the change name from `os.Args[0]`, wraps the callback in `BEGIN IMMEDIATE TRANSACTION`, inserts the `_schema_version` row on success, COMMITs / ROLLBACKs accordingly, and exits with the right status.

`.py` and `.sh` are deliberately not supported — Python contradicts E-894's move to Go; shell can't do anything `.sql` can't.

Rules:
- One task gets one change. If a task needs multiple destructive changes, split the task.
- Changes are NOT required to be idempotent — the `_schema_version` marker handles re-run safety.
- A task can have no change file at all if it's purely additive (the new tables / columns go in `schema.sql`).

### Application point: just land

Application is driven by `git diff` against the branch base, not by any in-binary registry. Each branch only adds its own change(s); historical changes are already in main and already applied to the DB. There's nothing to iterate.

`just land`, after rebase and before ff-merge:

```sh
for f in $(git diff main..HEAD --diff-filter=A --name-only -- internal/schema/changes/); do
    endless db apply-change "$f"
done
```

`endless db apply-change <path>` is a small Go-side dispatcher that handles both file types:

- **`.sql`**: open DB, `BEGIN IMMEDIATE TRANSACTION`, exec the file's content, `INSERT INTO _schema_version (name, applied_at) VALUES (basename, ...)`, `COMMIT`. ROLLBACK on any error.
- **`.go`**: `go run <path>` as a subprocess. The script uses the `runner` helper (above) to do its own `BEGIN IMMEDIATE` + author callback + `_schema_version` insert + `COMMIT` atomically. Dispatcher propagates the subprocess exit code.

Each change's effects AND the `_schema_version` row insert commit together or neither does — for both `.sql` (in the dispatcher's transaction) and `.go` (in the runner's transaction).

If a `just land` invocation fails partway through (script errors), previously-applied changes in the same invocation stay applied (each is its own transaction). The failed change is rolled back. On retry, `apply-change` sees the already-applied marker in `_schema_version` and skips it (the dispatcher checks before opening the transaction).

Order of landing equals order of application. Two branches with conflicting changes collide at rebase time on the `changes/` directory (a real, surfaced conflict), not later via mysterious data corruption.

### Helper: `internal/schema/changes/runner`

Tiny library (~30-50 lines) that `.go` change scripts import. Single function:

```go
func Run(apply func(*sql.Tx) error)
```

Responsibilities:
1. Determine DB path (env or `--db` flag from the dispatcher).
2. Determine change name from `filepath.Base(os.Args[0])` (without extension).
3. Check `_schema_version` for the name; if present, log "already applied" and exit 0.
4. `BEGIN IMMEDIATE TRANSACTION`.
5. Call `apply(tx)`.
6. On error: `ROLLBACK`, log, exit 1.
7. On success: `INSERT INTO _schema_version (name, applied_at) VALUES (?, strftime(...))`, `COMMIT`, exit 0.

This is the only Go code under `internal/schema/changes/` — change files themselves live as siblings to `runner/`. No registry, no `init()`, no `RegisterSQL`.

### `_schema_version` reshape

Current shape:
```sql
CREATE TABLE _schema_version (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    runner     TEXT NOT NULL DEFAULT 'auto'
);
```

New shape:
```sql
CREATE TABLE _schema_version (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
```

Drop `version` and `runner`. The 12 existing rows are backfilled to the new shape via THIS task's own script: `internal/schema/changes/e-1459-reshape-schema-version.sql`. Backfill keys:
- V1 → `v01-legacy-plan-task-base-tables`
- V2 → `v02-drop-dead-tables-rename-tier`
- ... etc., one per existing row
- The `name` field of each existing row already has the human-readable description; preserve it in the new `name` PK as a `vNN-slug` form.

This task's own script is the proof-of-concept that the new model works on real data before anyone else writes one.

### Code deletion

- `internal/monitor/migrate.go`: delete entirely.
- `internal/monitor/db.go`: remove all `migrateV<N>` functions. Remove the `migrate()` call from `DB()`. Keep `hasTable`, `hasColumn`, `BackupDB`, the schema.sql exec, the connection PRAGMAs.
- `DB()` connection setup: still `db.Exec(schema.SQL)` on first connection (creates fresh-DB tables). Stop calling `migrate()`. Add: scan and apply any unapplied scripts in `internal/schema/changes/` — OR don't, and trust that `just land` is the only application point. Default: don't apply on connection; binaries trust the DB is at whatever state land left it.
- Tests: `baseline_test.go`'s `TestMigrateFreshDB_CreatesBaselineTables` and `TestMigrateIdempotent` rewrite to exercise `db.Exec(schema.SQL)` directly. The "idempotent re-run" property moves from the framework to schema.sql itself (which already has it via `IF NOT EXISTS`).
- `src/endless/cli.py`: drop the `db migrate` subcommand. Replace with `db apply-changes` (or similar) if a manual application path is wanted; otherwise drop entirely.
- `src/endless/event_bridge.py`: drop `migrate_db()` shim.
- `cmd/endless-event/main.go`: drop the `migrate-db` subcommand and its `MigrateOpts` plumbing.

### sqlc adoption

In scope but kept minimal to avoid bloating this task:
- Add `sqlc.yaml` at repo root targeting `internal/schema/schema.sql`.
- Create `internal/queries/` directory with sqlc query files (one per query group).
- Generate into `internal/queries/sqlc/` (or whatever sqlc's default is).
- Convert ONE representative query (e.g., one of the more-used SELECTs in `internal/monitor/`) to prove the pipeline works.

Out of scope: converting every hand-written SQL in the codebase to sqlc. That's a separate follow-up.

### BackupDB

Currently auto-runs inside `migrate()` before any version bump. Without the framework, the auto-trigger is gone. Keep `BackupDB()` as a callable function with no auto-trigger. Available for use from `just land` (back up before applying scripts), or from a manual `endless db backup` command.
 Wire `just land` to call it as the first step of its apply-changes phase. Cheap insurance for the script-fails-midway case.

## Tasks obsoleted on land

Marked for close-with-outcome when E-1459 lands:
- **E-1380**: surface errors in migrateV* — code gone.
- **E-1453**: --dry-run silently applying — flag and command both removed.
- **E-1454**: RequiresRebuild framework gate — gate removed.

Mechanism-changed, not obsoleted:
- **E-1404**: AUTOINCREMENT PRIMARY KEY migration becomes the script `internal/schema/changes/e-1404-autoincrement-primary-keys.sql`. Task stays open; implementation route changes.

## Critical files

- `internal/monitor/migrate.go` — delete
- `internal/monitor/db.go` — remove migrate* functions; keep schema exec + helpers + BackupDB
- `internal/monitor/baseline_test.go` — rewrite tests to drive schema.sql directly
- `internal/monitor/project_next_test.go` — same
- `internal/schema/schema.sql` — schema source of truth; absorb any current `migrateV<N>` table-creates that belong in steady-state
- `internal/schema/changes/` — new directory; holds change files (`.sql` and `.go`) plus the `runner/` helper subpackage
- `internal/schema/changes/runner/` — new tiny helper package for `.go` change scripts
- `internal/schema/changes/e-1459-reshape-schema-version.sql` — new; the reshape + backfill change (proves the new model on real data)
- `src/endless/cli.py` — drop `db migrate` subcommand; add `db apply-change <path>`
- `src/endless/event_bridge.py` — drop `migrate_db` shim; add `apply_change` shim
- `cmd/endless-event/main.go` — drop `migrate-db` subcommand; add `apply-change <path>` dispatcher
- `justfile` — `land` recipe gains a `BackupDB` + git-diff-driven `apply-change` loop between rebase and ff-merge
- `sqlc.yaml` — new
- `internal/queries/*.sql` — new (sqlc query files; minimal initial population)

## Risk: half-implementation

If sqlc adoption stalls mid-implementation, the framework removal could land with no replacement query system. Mitigation: sqlc is the LAST phase of this task. If work has to stop, the framework removal lands first (queries stay hand-written), sqlc becomes its own follow-up. The schema.sql + changes/ + apply-on-land model is independently usable.

## Verification

1. Existing tests pass after refactor: `go test ./internal/monitor/ -v`. The two old framework tests rewrite to drive schema.sql directly.
2. Fresh-DB init: rm `endless.db`, start endless, observe schema.sql creates everything, `_schema_version` is empty (no changes applied to a brand-new DB).
3. Existing-DB upgrade: backup current `endless.db`, run new binary on it, then run `just land` from a branch that has the E-1459 reshape change. Observe the 12 historical rows are backfilled to the TEXT-name shape with no data loss.
4. Apply-on-land: create a throwaway branch with a no-op change (`RegisterSQL("e-test", "SELECT 1;")`) in `internal/schema/changes/`, run `just land`, observe the change applies and a row appears in `_schema_version`.
5. Idempotency: re-run `just land` on the landed branch; the change is recognized as applied and skipped, no error.
6. sqlc: `sqlc generate` produces compilable Go code from the one representative query; the converted call site still passes its tests.

## Out of scope

- Migrating every hand-written SQL query to sqlc (one representative is enough to prove the pipeline; the rest is a follow-up task).
- Choosing a future production migration tool (atlas, golang-migrate, etc.) — that's "when we ship to users", not now.
- Removing `BackupDB()` from the codebase (kept as a callable utility per recommendation above).
- Renaming `_schema_version` table (the name still fits; changing it would inflate the diff for no clarity gain).
