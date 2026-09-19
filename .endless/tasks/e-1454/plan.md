# E-1454 prompt: Retire `RequiresRebuild` framework gate

## Symptom

When a registered migration sets `RequiresRebuild: true`, **every** fresh DB (tests, new installs) fails to open with `migration requires --force-rebuild`. There is no way for a fresh DB to skip a rebuild that would be a no-op against its just-created tables. Discovered during E-1396 when V11 was first written with `RequiresRebuild: true` (the original plan called for it as belt-and-suspenders alongside `BEGIN IMMEDIATE TRANSACTION`); the change immediately broke 53 fresh-DB tests, forcing the flag to be dropped and the migration to rely on transaction atomicity alone.

Reproducer (synthetic):

```go
// Add to migrations registry:
{Version: 12, Name: "foo", Apply: migrateV12, RequiresRebuild: true},
```

Run `just test`. Every Python test that opens a fresh DB fails with `auto-migrate ...: migration requires --force-rebuild`.

## Root cause

`internal/monitor/migrate.go:263-270`:

```go
if m.RequiresRebuild && !opts.AllowRebuild {
    err = doterr.NewErr(
        ErrRequiresRebuild,
        doterr.IntKV("version", m.Version),
        doterr.StringKV("name", m.Name),
    )
    goto end
}
```

The gate is a flat boolean check. It does not consult:

- Whether the current DB actually has the OLD-shape table that needs rebuilding.
- Whether this is a fresh DB (`user_version = 0`) where every prior migration is also pending and the "rebuild" is logically a "create with the right shape from the start".
- The migration's own `Apply` function (which often *could* tell, since well-written rebuild migrations check `hasColumn` / `hasTable` and no-op when not needed).

The flag is correctly named for its intent — "this migration rebuilds a whole table, so don't run it without operator approval" — but its enforcement is over-broad. It refuses to run on populations where the rebuild is trivially safe (no rows yet) or trivially unnecessary (table already at target shape).

## Fix: retire `RequiresRebuild` entirely

Replace the gate with a non-negotiable contract for rebuild migrations:

1. **Atomicity**: rebuild migrations MUST wrap their DDL in `BEGIN IMMEDIATE TRANSACTION ... COMMIT`. Concurrent writers block on the RESERVED lock until COMMIT; partial failure ROLLs BACK to the prior shape.
2. **Idempotency**: rebuild migrations MUST check whether the target shape is already in place (`hasColumn` / `hasTable` / `PRAGMA table_info`) and no-op if so. Fresh DBs that come up at the target shape via earlier migrations skip the rebuild trivially. Partial-failure reruns converge.
3. **Backup**: `BackupDB()` continues to run before auto-migrate (`migrate.go:244`), giving a recovery point regardless.

E-1396's V11 already implements all three. The pattern is proven; the framework just needs to stop second-guessing it.

### Changes

- Remove `RequiresRebuild` field from `Migration` struct (migrate.go:33-38).
- Remove the gate at migrate.go:263-270.
- Remove `AllowRebuild` from `MigrateOpts` (migrate.go:75-89), the `RequiresRebuild` references in `MigrateOpts` docs, and the `-force-rebuild` flag from `cmd/endless-event/main.go`.
- Remove `RunnerForceRebuild` from the `RunnerLabel` enum (migrate.go:67-72) and its emission paths.
- Update the `Migration` docstring (migrate.go:18-32) to encode the new contract: rebuild migrations are transactional, idempotent, and operate like any other migration.
- E-1396's V11 is unchanged — it already satisfies the contract.

### Considered alternatives

- **Introspect target shape per-migration via a `NoOpCheck` hook**: surgical, but pushes correctness onto every rebuild-migration author; if they get the check wrong, the safety promise is silently broken.
- **Fresh-DB lineage detection** (auto-allow rebuilds when `user_version = 0` and `_schema_version` is empty): pragmatic but borderline cases (partially migrated DBs from the legacy stopgap) misclassify.
- **Per-call `AllowRebuild` policy plumbed in from the caller**: same lineage-call burden, no new fields.

None of these address the fundamental issue that `RequiresRebuild` doesn't solve a problem the transaction + idempotency contract doesn't already solve. The framework was over-engineered; the right fix is to trust the SQLite primitives.

### What's lost

The "human in the loop" affordance for risky migrations goes away. If a future migration genuinely needs operator scheduling (e.g., a multi-hour rebuild on a large table), it can:

- Use `Target` to gate auto-migrate, so the operator must explicitly bump the version cap, OR
- Live as a separate `endless db migrate-heavy` verb outside the auto-migrate registry, OR
- Be split into smaller idempotent steps that the registry can apply incrementally.

These give the same control without the foot-gun.

## Verification

1. Register a synthetic rebuild migration that's idempotent and transactional. Run the test suite. Expected: all tests pass on fresh DBs.
2. Against an existing DB (user_version=9 from before V10 shipped), invoke auto-migrate via opening any binary. Expected: V10/V11/synthetic apply cleanly.
3. Re-run auto-migrate on a DB already at the target version. Expected: idempotency guard short-circuits; no schema changes.
4. Concurrency: open the DB from two processes simultaneously while V11 (or a synthetic rebuild) is pending. Expected: BEGIN IMMEDIATE serializes; both processes end up at the new schema with no data loss.
5. Add a regression test in `internal/monitor/migrate_test.go` that drives a fresh DB through a registry containing a rebuild migration and asserts the migration applies without operator intervention.

## Related

- E-1396 — surfaced this gap while planning the V11 rebuild; original plan included `RequiresRebuild: true` until it broke 53 tests. V11 already demonstrates the transactional + idempotent pattern this proposal codifies.
- E-1453 — `migrate-db -dry-run` bug, related framework hygiene.
- E-1380 — migration error-handling hygiene, separate scope.
