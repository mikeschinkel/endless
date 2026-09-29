# Plan — apply a landing branch's goose migrations before task.landed

## Why

See the analysis. `worktree land` applies only new `internal/schema/changes/`
files. The branch's goose migrations reach main only when an installed binary
next connects, so the candidate endless-go that emits `task.landed` (Step 6) can
run against an unmigrated database. E-2188 hit this: main advanced, and the
landing was not recorded.

## Decisions (Mike, 2026-09-29)

1. **Mechanism: `endless-migrate up`.** A second subcommand beside `apply`. It
   runs `schema.MigrateContext` (goose Up, then seeds) against the database the
   caller named (`--db main` / `--db-dir`; `--db sandbox` still refused).
   `internal/schema` links only the migration machinery, so
   `TestMigrateExecutable_LinksNothingButTheMigrationMachinery` must keep
   passing unchanged. Update `TestMigrateExecutable_OffersOnlyApply`, and the
   package doc's "one subcommand" paragraph, to exactly `apply` + `up`. Output
   takes the JSON shape `apply` prints (`schemachange.Result`, or a sibling
   shape carrying from/to versions).
2. **Trigger: every self_dev land.** Always build migrate-bin (`just
   migrate-bin`) at Step 4.6, before the ff-merge, so a tree that cannot compile
   it aborts with base and the database untouched. Always run `up` at Step 5.5.
   It is a no-op when the database is current. Non-self_dev projects are
   unchanged.
3. **Order: migrations first by default, overridable per task.** Default at
   Step 5.5: one backup, then `endless-migrate up`, then the branch's
   `changes/` files through the existing `apply` loop. A task whose change
   files must run first says so in its own directory on the landing branch,
   in a `land.toml` (see "land.toml" below):

   `.endless/tasks/e-<id>/land.toml`
   ```toml
   [self_dev]
   schema_order = "changes-first"   # default: "migrations-first"
   ```

   Land reads it from the worktree (the landing branch), since the agent that
   wrote the change is the one who knows, not whoever runs the land. Any other
   value refuses the land BEFORE the ff-merge, naming the file and the two legal
   values. A missing file, or a file with no `[self_dev]` table, means the
   default.
4. **E-2020: independent, relates_to.** Spawnable now.

## land.toml (Mike, 2026-09-29)

`land.toml` is the home for per-branch LAND settings, not a one-off for this
key. "How this branch lands" is a general concept for any project — a target
branch, a merge strategy, pre-land gates, post-land steps — and a branch file is
the right place for it: it travels with the branch, rebases with the code it
describes, is visible in the reviewed diff, and is what the land reads at the
moment it lands. A DB task field is detached from that code.

Shape rules this task establishes:

- **Keys live in tables (sections), never at top level**, so later settings
  (`[merge]`, `[gates]`, ...) each get their own.
- **Endless-internal settings go under `[self_dev]`.** `schema_order` is
  meaningful only to Endless landing itself (goose migrations vs `changes/`
  files), so it must not sit at the top level of a file meant for every project.
- **Intended layering** (NOT built here): a project-wide `.endless/land.toml`
  with the same keys, overridden per task by `.endless/tasks/e-<id>/land.toml`.

Scope for THIS task: read the per-task file only, and honour
`[self_dev].schema_order`. Do not build the project-wide file or any other key.
Unknown tables and keys: refuse the land before the ff-merge, naming them — a
typo in a landing instruction must not be silently ignored.

Document it as the place for per-branch land settings: in
`.endless/tasks/CLAUDE.md` (a second task-owned file beside
`verify.toml`/`verify.sh`, written on the task branch), and in `endless guide
orchestration` under Landing the work.

## Also in scope

- **One backup** before whichever step runs first, not one per step. Reuse the
  pre-apply backup `_apply_branch_schema_changes` already takes; do not add a
  second.
- **The record-only re-run** (`just land` after "main was advanced, but recording
  the landing failed") runs `up` before emitting `task.landed`. That is the
  E-2188 recovery path, and it must not depend on an incidental migration.
- **Failure after the ff-merge** is surfaced like a `changes/` failure today:
  main advanced, the database lags, re-run fixes it. No rollback of the merge.

## Verify

- Unit (Go, `cmd/endless-migrate` / `internal/schemachange`): `up` on a database
  behind the embedded set brings it to `LatestVersion` and seeds it; `up` on a
  current database is a no-op; `--db sandbox` is refused; the surface test
  allows exactly `apply` and `up`.
- Unit (Python, land): Step 5.5 calls `up` before `task.landed` on every self_dev
  land; the order follows `land.toml`'s `[self_dev].schema_order` (default,
  `changes-first`, an invalid value and an unknown key/table each refused
  before the merge); the record-only path calls `up`; one backup.
- End to end (verify suite, throwaway repo + database): a branch adding a
  migration whose code writes the new column during `task.landed` lands AND
  records the landing in one run. It is the E-2188 failure reproduced, and it
  must fail against main before this change.
