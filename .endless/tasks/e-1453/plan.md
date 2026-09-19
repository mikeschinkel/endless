# E-1453 prompt: Fix `migrate-db -dry-run` silently applying because `monitor.DB()` auto-migrates on open

## Symptom

`endless-event migrate-db -dry-run` reports `{"applied":null,"skipped":[...,"already applied"]}` instead of `{"applied":[...,"dry-run"],"skipped":[]}` — but the database has actually been migrated. Discovered during E-1396 verification: a `-dry-run` invocation against a DB at `user_version=10` returned all-skipped, yet `user_version` came out at `11` and `session_tasks` had the new id column.

Reproducer:

```sh
# Against a DB at user_version=N where N < CurrentSchemaVersion:
endless-event migrate-db -dry-run
# Output suggests no-op. Real effect: full migration applied.
endless sql "SELECT * FROM pragma_user_version()"
# user_version has advanced to CurrentSchemaVersion.
```

## Root cause

`internal/monitor/db.go` — `DB()` opens the connection and unconditionally invokes `migrate(dbConn, MigrateOpts{Runner: RunnerAuto})`:

```go
func DB() (*sql.DB, error) {
    dbOnce.Do(func() {
        ...
        if _, err := migrate(dbConn, MigrateOpts{Runner: RunnerAuto}); err != nil {
            dbErr = fmt.Errorf("auto-migrate %s: %w", path, err)
            ...
        }
    })
    return dbConn, dbErr
}
```

`cmd/endless-event/main.go` reads the `-dry-run` flag, but by the time it calls `monitor.DB()` to get a handle, the real migration has already run via the embedded auto-migrate. The framework's `DryRun` field works correctly for the explicit `Migrate(opts)` path; it's the auto-migrate-on-first-open path that ignores it. So every CLI verb that needs the connection (which is all of them) silently advances the schema before any flag is consulted.

## Fix: plumb a dry-run signal into `DB()`

Change `DB()` to accept `MigrateOpts` (or the subset that affects auto-migrate behavior). The CLI parses flags first, then calls `DB(opts)`. Auto-migrate is skipped or runs dry per the caller's intent.

Sketch:

```go
// Existing call sites continue to work via a thin wrapper:
func DB() (*sql.DB, error) { return DBWith(MigrateOpts{Runner: RunnerAuto}) }

// New entry point that respects the caller's intent:
func DBWith(opts MigrateOpts) (*sql.DB, error) {
    dbOnce.Do(func() {
        ...
        if _, err := migrate(dbConn, opts); err != nil { ... }
    })
    return dbConn, dbErr
}
```

`cmd/endless-event/main.go` becomes:

```go
flag.Parse() // includes -dry-run
opts := MigrateOpts{Runner: RunnerAuto, DryRun: *dryRun}
db, err := monitor.DBWith(opts)
```

Other CLIs that don't care continue to call `monitor.DB()` and get today's behavior.

### Implementation notes

- `dbOnce.Do` is a `sync.Once`, so the first caller's opts win. If two callers race with different opts, the second silently gets the first's behavior. In practice each binary process has one entry point setting opts, so this is fine — but the docstring should call it out.
- The other `MigrateOpts` fields (`AllowRebuild`, `SkipBackup`, `Target`) can ride along on the same plumbing for free; `migrate-db`'s `-force-rebuild` and `-target` flags become non-noops via the same mechanism.

## Verification

1. Register a synthetic migration that mutates the schema noticeably (e.g., `CREATE TABLE _dryrun_canary`).
2. Take a DB at `user_version = CurrentSchemaVersion - 1`.
3. Run `endless-event migrate-db -dry-run`.
4. Assert: `pragma user_version` unchanged, `_dryrun_canary` does NOT exist, and the dry-run report lists the migration with reason `"dry-run"`.
5. Run `endless-event migrate-db` (no flag) and confirm the migration applies normally.
6. Add a regression test in `internal/monitor/migrate_test.go` that exercises the dry-run path against a non-final-version DB.

## Related

- E-1396 — surfaced this bug during V11 verification.
- E-1380 (referenced in migrate.go comments) — migration error-handling hygiene, separate scope.
