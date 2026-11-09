# Make just verify record its pass in the main database

## Why

`just verify` runs `endless --db sandbox task verify <id>`. `--db sandbox` does
two things at once: it runs the worktree's own code and runner (E-1510), and it
pins the worktree's sandbox database. The front door records a pass in the
pinned database, so a pass from `just verify` is recorded in the sandbox, while
`worktree land` reads main and refuses. Found in E-2278, where it blocked a land
twice; every self-dev task verified with `just verify` hits it.

Self-dev only. `just verify` is a dev recipe, and a project that is not
self-dev has one database, so `endless task verify` records where land reads.
Nothing here changes product behaviour.

Recording the pass in main while the worktree's code runs is not an option:
ED-1601 lets only the installed binary write the main database.

## Decision (Q1, answered: A)

`just verify` runs `endless --db main task verify <id>`: main's installed code
runs the suite and records the pass in main. This is what a plain
`endless task verify E-<id>` already does (`config.default_db_to_main()` in
`src/endless/verify_cmd.py`).

What changes: a branch's own runner no longer runs its suite under
`just verify`. That matters only for a branch that changes the verify
machinery (`internal/verify`, `internal/verifycmd`, `src/endless/verify_cmd.py`,
`.endless/tasks/_harness.sh`); its suite runs its own candidate
`bin/endless-go verify` from inside, against a fixture project, as E-2023,
E-2243 and E-2278 do (consistent with ED-1596 and ED-1601).

## Changes

1. `justfile`, `verify` recipe: the last line becomes
   `endless --db main task verify "$tid"`. Rewrite the recipe's comment: it now
   resolves the id, cds into the task's worktree, and records in main; drop the
   E-1510 "exercises the CANDIDATE runner" rationale and point a
   runner-changing branch at the drive-the-candidate pattern.
2. `docs/guide/orchestration.md`, the self-dev note under "Running one":
   replace the `--db sandbox` sentence with the above — `just verify` records
   in main, and a branch that changes the verify machinery drives its own
   candidate runner from its suite.

## Interaction with E-2583

E-2583 removes `unlanded` as a stored status but keeps a pass as a record.
This task is about WHERE that record goes, so it holds under either design.
Whichever lands second adjusts nothing here beyond wording.

## Out of scope (left open on purpose)

Testing a branch that changes how a pass is RECORDED (the front door's own
recording code) through the land gate. Main's front door records, so such a
branch's recording code is exercised only by its own tests until it lands.
Not handled until a task needs it.

## Verification

Suite at `.endless/tasks/e-2282/verify.sh`:

- `just --show verify` ends in `endless --db main task verify "$tid"` and
  contains no `--db sandbox`.
- The guide note names `--db main` and the drive-the-candidate pattern, and
  no longer says `just verify` selects the worktree's `endless-go`.

The behaviour itself (a `just verify` pass lets `just land` proceed) is
confirmed by the user's own handoff run: `just verify`, then `just land`.
