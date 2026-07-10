# E-1434: Add project_next tables and migration

## Spec source

Full design lives in E-1421's plan at `<main>/.endless/plans/E-1421.md`. Read the "SQL schema (5 tables)" and "Concurrency model" sections — they ARE the spec for this sub-task. This file just narrows the scope.

## Scope

1. Create the 5 tables exactly as defined in E-1421's plan:
   - `project_next` (one row per project; header + FK target)
   - `project_next_lanes` (lanes within a project's curated list)
   - `project_next_items` (items within a lane, ordered by position)
   - `project_next_pending` (auto-added urgent tasks awaiting curation)
   - `project_next_revisions` (audit trail of revisions; in scope per design decision)

2. Write the migration script following existing endless DB migration patterns. Inspect recent migrations to match style (file naming, transaction wrapping, idempotency, version registration).

3. Tests covering:
   - All 5 tables created with expected columns and constraints
   - Each starts with `id INTEGER PRIMARY KEY` (house rule)
   - FK `ON DELETE CASCADE` actually cascades for the four child tables
   - UNIQUE constraints reject duplicates as specified
   - Migration is forward-only safe (re-running is a no-op)

## Out of scope

- Any CLI commands — separate sub-tasks E-1436 (revise), E-1438 (move), E-1439 (briefing), E-1440 (read), E-1441 (migrate --inferred)
- Hook subscribers — E-1437
- A `project_next_lock` table — not present in this design; concurrency is via `BEGIN IMMEDIATE TRANSACTION`, applied in revise/move sub-tasks
- Compaction logic — explicitly deferred (YAGNI per the resolved questions section in E-1421's plan)

## Verification

1. From a fresh DB, run the migration. All 5 tables exist with the documented columns and constraints.
2. Re-run the migration. No errors, no schema changes — idempotent.
3. Insert into all 5 tables in dependency order. Delete the `project_next` row. Confirm child rows in lanes/items/pending/revisions are gone (CASCADE works).
4. Attempt to insert a duplicate `(project_next_id, lane_id)` into `project_next_lanes`. Expect rejection from the UNIQUE constraint.
5. Attempt to insert a `project_next_items` row with a non-existent `project_next_lane_id`. Expect FK rejection.

## Instructions for the implementing session

1. Read E-1421's plan at `<main>/.endless/plans/E-1421.md` — at minimum the SQL schema and concurrency sections.
2. Inspect existing endless DB migrations to match style and infrastructure (where the migration files live, how they're registered, how versioning works).
3. No separate `.endless/plans/E-1434.md` file required — E-1421's plan covers the spec. Implementation notes go in the task outcome when flipping to `verify`.
4. Flip to `verify` when done, with the verification block above included verbatim in the handoff message so Mike has copy-paste-ready steps.
