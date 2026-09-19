## Scope

Delete the per-ticket change-file mechanism now that goose owns the schema:

- internal/schema/changes/ -- 38 files (15 .go, 23 .sql) plus the runner package, ~2,700 lines.
- The _schema_version table, through a NEW migration, 00002, which drops it — the first real forward migration. Remove its declaration from schema.sql in the same change; TestMigrate_MatchesSchemaSQL fails if only one of the two moves. Never edit 00001_baseline.sql to do this: the real ledger is already recorded at version 1, so editing the baseline would change nothing there and would make every fresh database disagree with it. (Corrected 2026-09-17: an earlier version of this analysis said to edit the baseline.)
- `endless-go event apply-change`, `endless db apply-change`, event_bridge.apply_change.
- db.py's dead legacy migrator: _migrate, _migrate_v2/v3/v5/v6, _should_auto_migrate, the ENDLESS_AUTO_MIGRATE environment variable, and the _has_table/_has_column helpers that serve only them. Its own docstring says it was slated for removal in E-894 Phase 5, and internal/monitor/migrate.go -- the thing that was to replace it -- was deleted by E-1459. It short-circuits at PRAGMA user_version >= 6 and the real ledger is at 12, so it does nothing there. It is NOT inert elsewhere, and it is a PRODUCT problem rather than a tidy-up: nothing in the ladder ever sets user_version above 5, and every database Go builds starts at 0 — every sandbox, and every fresh install on someone else's machine. Such a database takes the whole ladder, preceded by a _backup_db() attempt (throttled to one per 60s), on every Python connect, forever. (Corrected 2026-09-17: an earlier version said the backup runs before the short-circuit test; it runs after it, and the effect is as described here.)

## The one real design step

E-2088 landed 2026-09-16 (901e589) and built cmd/endless-migrate around change FILES: `endless-migrate apply <change-file>`, invoked once per changed file by `worktree land` at Step 5.5, with the file list computed at Step 4.6 and the executable built by `just migrate-bin` in between. Under goose there are no change files to enumerate.

So the executable's subcommand becomes `up`, and the land's question changes from "which change files does this branch add" to "does this branch carry a migration the database has not got" -- schema.LatestVersion() (what the binary carries) compared against the database's recorded version, rather than a directory walk. internal/schemachange is then either deleted outright or reduced to whatever `up` still needs; the `.go` change runner goes with the change files it served.

Do not lose what E-2088 established while reworking it: the build-then-apply ordering (build at 4.6 while base is still untouched, apply at 5.5 between the ff-merge and the record), a build failure aborting with nothing merged and nothing migrated, and an apply failure producing the post-merge message that says re-run rather than restore.

## What E-2019 already verified for this task

internal/schemachange/executable_test.go asserts `go list -deps ./cmd/endless-migrate` links only dbcontext + schemachange + the cmd -- the property that a migration tool carries no code expecting a schema. internal/schema imports nothing but stdlib and github.com/pressly/goose/v3, so it satisfies that property as-is and needs no restructuring. It should REPLACE internal/schemachange in the allowlist, not be added beside it: the test's own rule is that the allowlist is never widened, and once change files are gone schemachange has nothing left to do. (Corrected 2026-09-17: an earlier version said the allowlist simply gains it.)

## Constraint

schema.sql must survive this task. It is still the readable artifact, and E-2021 is what turns it into a generated one; deleting it here leaves it deleted and unreplaced. Deleting only its _schema_version declaration is correct and sufficient.


