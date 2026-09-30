# Plan — delete the change-file mechanism; goose is the only way schema moves

Measured on main 2026-09-30 (b8c40ce0f). The analysis predates E-2192 and E-2020;
where they disagree, this plan wins.

## Sequencing

Starts only after **E-2020 has landed**. Both touch `src/endless/db.py` and the
connect/land migration path, and this plan relies on E-2020's `endless db
upgrade`. (Recorded as prose until E-2164's `conflicts_with` exists.)

## What is already done, and must not be redone

- **`endless-migrate up` exists** (E-2192) and every self_dev land runs it at
  Step 5.5, after the ff-merge and before `task.landed`, behind one backup, and
  on the recovery re-run. The analysis's "the subcommand becomes `up`" is done.
  Keep E-2088's guarantees: build at 4.6 before base moves, a build failure
  aborts with nothing merged or migrated, an apply failure after the merge says
  re-run rather than restore.

## Decisions (Mike, 2026-09-30)

1. **land.toml stays, with zero keys.** Delete `[self_dev].schema_order` — with
   one migration step there is nothing to order. Keep the loader
   (`_read_schema_order`, renamed to what it now is: reading/validating land
   settings), `LAND_SETTINGS_FILENAME`, and the refusal of any unknown table or
   key before the ff-merge, naming it. `_LAND_SETTINGS_KEYS` becomes empty, so
   ANY key is refused — including a `schema_order` a branch still carries, with
   a message saying the key was retired by E-2158 and to delete it. The docs keep
   describing land.toml as the home for per-branch land settings (sections;
   Endless-internal keys under `[self_dev]`); only the `schema_order` example
   and "new change files are applied" text go.
2. **The legacy Python migrator is deleted, and a pre-v6 database is refused
   explicitly.** Delete `_should_auto_migrate`, `ENDLESS_AUTO_MIGRATE`,
   `_migrate`, `_migrate_v2/v3/v5/v6`, `_backup_db` if nothing else uses it, and
   `_has_table`/`_has_column` only where nothing else uses them (`get_db`,
   `_schema_error_hint` use `_has_table`). Remove `ENDLESS_AUTO_MIGRATE` from
   `tests/conftest.py`.

   The refusal lives in Go, inside `schema.MigrateContext`, BEFORE goose runs —
   the one path every migrator takes (`monitor.DB`, `endless-migrate up`,
   `endless db upgrade`, the sandbox seeder, the projector). Condition: the
   database has a `projects` table, has NO `goose_db_version` table, and `PRAGMA
   user_version` < 6. Such a database predates both versioning schemes; running
   goose's idempotent baseline on it would create missing tables, miss missing
   columns, and stamp it version 1 — hiding its age. Refuse with an error naming
   the database, its user_version, and that it is too old to upgrade in place.
   Every database Go builds has `goose_db_version`, and the real ledger does, so
   neither is affected. Python gets no check: it builds databases through Go.
3. **`_incomplete_schema_hint` points at `endless db upgrade`.** On a missing
   column or table, say the database's schema is older than this endless
   expects, name the missing object, and name `endless db upgrade` (E-2020).
   Delete the change-file lookup behind it: `_unapplied_changes`,
   `_change_files`, `_changes_naming`, `_CHANGES_DIR`, `_SCHEMA_PATH`.

## Delete

- `internal/schema/changes/` — 41 change files and `runner/`.
- `internal/schemachange/` entirely. `endless-migrate` keeps only `up`: remove
  `apply`/`runApply`, rewrite its package doc and usage, and give its `result`
  type a home that is not schemachange (or have `up` print its own shape).
  In `internal/schemachange/executable_test.go`'s successor (move the test beside
  `cmd/endless-migrate`), the allowlist loses `internal/schemachange` and is
  otherwise unchanged; the surface test allows exactly `up`.
- `endless-go event apply-change` (`eventcmd.runApplyChange`, its usage line),
  `event_bridge.apply_change`, `endless db apply-change` (`cli.py`), and the
  apply-change routing comments in `config.py`.
- Land: `_branch_schema_changes`, `_migrate_change`, the `apply_changes` step
  and the order logic in `_apply_branch_schema_changes` (it becomes: backup, then
  `up`). `_post_merge_failure`'s "gated by _schema_version" text becomes the
  goose equivalent. `just migrate-bin`'s comment and the land recipe comments.
- `_schema_version`: a NEW goose migration with the next free number drops it,
  and schema.sql loses its declaration in the same change
  (`TestMigrate_MatchesSchemaSQL`). Never edit `00001_baseline.sql`.
  `internal/monitor/baseline_test.go` stops expecting it.

## Keep

- `schema.sql` (E-2021 generates it later). `docs/research-*.tsv` and landed
  verify suites under `.endless/tasks/` are records — leave them.
- `.endless/migrations/` is a different mechanism (data backfills); only fix a
  comment there if it names `schema/changes`.

## Comment and doc sweep

`git grep` outside `.endless/` for `schema/changes`, `schemachange`,
`apply-change`, `_schema_version`, `change file`, `ENDLESS_AUTO_MIGRATE`, and fix
or delete each: comments in `internal/events/decision.go`,
`internal/events/repair_claim_bindings.go`, `internal/faults/codes.go`,
`internal/monitor/project_path.go`, `internal/eventcmd/event.go`,
`src/endless/project_path.py`; the self_dev block of
`docs/guide/orchestration.md`'s land.toml section.

## Tests

Delete or rewrite: `internal/schemachange/*_test.go`, `internal/eventcmd`'s
apply-change test, `tests/test_db_gate.py::test_db_apply_change_pins_main_from_sandbox`,
`tests/test_event_bridge_worktree_binary.py`'s apply_change case,
`tests/test_worktree_land_schema_apply.py`, the change-file halves of
`tests/test_worktree_land_migrate_exec.py`, `tests/test_db_error_diagnostic.py`
(now asserting the `db upgrade` hint), `tests/test_guide_conditionals.py`'s
`schema_order` assertion, and the `_schema_version` expectations in the monitor
tests listed by the measurement.

## Verify

- `internal/schema/changes/` and `internal/schemachange/` do not exist; `git grep`
  outside `.endless/` finds none of the swept names.
- `endless-migrate` offers exactly `up`; the link-allowlist test passes without
  schemachange.
- A self_dev land carrying a goose migration migrates and records in one run
  (E-2192's end-to-end, still green).
- land.toml: an empty or absent file lands; a file with `[self_dev]
  schema_order` is refused before the merge with the "retired by E-2158" message;
  any other key is refused.
- A fresh database and the real-ledger-shaped fixture reach the new version with
  `_schema_version` gone; schema.sql matches.
- The pre-v6 refusal: a fixture with `projects`, no `goose_db_version` and
  user_version 5 is refused by `schema.MigrateContext` (so by `monitor.DB` and
  `endless-migrate up`) with the too-old message, and is left untouched
  (sqlite_master identical before and after); a database WITH
  `goose_db_version` at user_version 0 migrates normally.
- A Python read hitting a missing column prints the `endless db upgrade` hint
  naming the object.
- Python connect no longer runs a migration ladder: a user_version-0 database
  is not modified by a Python read.
