Also removes E-1818's schema-passive mode, which becomes unnecessary once no connect applies schema: the mode exists to stop a binary mutating a DB it merely opened, and nothing needs suppressing when nothing applies.

## The real ledger and schema version 1

E-2019 landed and the ledger is recorded at goose version 1. Measured then, the ledger did NOT match what version 1 declares: a pre-rename `tasks.status` default of 'needs_plan', a `tasks_updated_at` trigger missing its no-op-write guard, an unused `tasks.focus_id`, 11 tables left by removed features (two of them from change files recorded as applied whose DROPs never took effect), and 5 orphaned rows.

That was cleaned up by hand on 2026-09-17, deliberately with no task (Mike's call: data cleansing needs no record). Backups taken before each step are in ~/.config/endless/backups/. Measured after, against a database freshly built from schema.sql:

- Objects: identical set. Nothing extra, nothing missing.
- Columns, types, defaults and foreign keys: identical on every table.
- `PRAGMA foreign_key_check`: 0 violations. `integrity_check`: ok.
- Still different, both cosmetic: column ORDER on tasks, decisions, task_landings and errors (ALTER-appended rather than declared order), and `session_statuses_session_recent_idx` spelled `(session_id asc, created_at desc)` rather than `(session_id, created_at DESC)` — the same index.

Consequence for this task: if the direction rules ever want more than the recorded version integer, a SEMANTIC shape comparison (object set, pragma_table_info columns/types/defaults, pragma_foreign_key_list) now passes on the real ledger. An exact comparison of sqlite_master text still does not, and never will without rebuilding four tables to reorder columns — so do not build one.

## What E-2019 leaves for this task to build on

- schema.Migrate(db) is the single way a database acquires its schema (monitor.DB, the sandbox seeder, the ledger projector, tests, and Python via `endless-go event migrate`).
- schema.DBVersion(ctx, db) reads the version a database is recorded at; schema.LatestVersion() reads the highest version the BINARY carries. The direction rules are a comparison of those two.
- schema.Seed(db) applies the enum mirrors and is called on every Migrate. It is deliberately NOT a migration: it preserves E-1659's self-heal. When this task stops applying schema on connect, decide explicitly whether Seed keeps running — the enum gates in monitor.DB() depend on it.
- Migrate also turns on foreign key enforcement for the connection, because schema.sql's leading PRAGMA used to do that as a side effect of being exec'd. Keep that property if Migrate stops being called on connect: something else has to own it.
- 00001_baseline.sql is the only migration allowed to be idempotent (every statement carries IF NOT EXISTS). A later migration must not copy that.
