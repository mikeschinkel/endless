# Fix `just land` failing the task_types integrity gate on enum-adding schema changes

## Symptom (observed landing E-1657)

`just land` for a branch whose schema change ADDS a mirrored-enum value
(`task_types` id=5 `brainstorm`) advanced main and applied the change, then
failed at "recording the landing":

```
Event write failed: ... task_types integrity check on ~/.config/endless/endless.db:
tasktype: task_types row id=5 slug="brainstorm" has no matching enum constant
```

Recovery was manual: `just install && just land E-1657` (rebuild the global
binary so its enum matches the now-advanced DB, then re-run the idempotent land
to record the ledger entry).

## Root cause

The land sequence applies the schema change (DB gains the new `task_types` row)
**before** the global `endless-go` binary is reinstalled. The "record landing"
step then writes its event with the **stale global binary**, whose
`internal/tasktype` enum has no id=5 constant yet. `monitor.DB()` runs
`tasktype.VerifyIntegrity` at startup and fails closed on "DB row with no
matching enum constant" — correct drift detection, wrong moment.

The `apply-change` step immediately prior already sidesteps this: it runs with
`PATH="$wt/bin:$PATH"` so it uses the **worktree** binary (which DOES have the
new constant). The record-landing step does not.

This is latent for every future mirrored-enum addition (task_types, and any
other Go enum whose values seed a SQL mirror), not specific to brainstorm.

## Recommended fix

Make the record-landing event write use the **worktree** binary, the same way
`apply-change` does (`PATH="$wt/bin:$PATH" endless-go event ...`). The worktree
is built (`just build`) before land, so its enum already matches the row the
change-file inserts. This keeps the integrity gate fully armed while closing the
stale-binary window.

Alternative considered: reinstall the global binary (`just install`) between the
schema-change apply and the record-landing step. Rejected as primary — it widens
land's global side effects mid-operation and is heavier than pointing one
subprocess at the worktree binary.

## Data residue from the E-1657 instance is BENIGN — do not "repair" it

The manual recovery (re-running `just land`) left two artifacts on E-1657, both
of which look like corruption but are NOT:

- A **duplicate `task.landed` event** in the db-ledger (the first attempt
  appended its event before the DB projection was blocked; the re-run appended a
  second).
- The live `task_landings` row carries an **orphaned merge_commit_sha**
  (`ff3e9cf5`, an amended-away "record ledger entry" commit) instead of the real
  ff merge (`937268f6`).

Neither is unique damage. Baseline check (2026-06-25): duplicate `task.landed`
events are routine — 21 tasks have them (up to 6 each), no UNIQUE on
`task_landings.task_id`. Dangling landing shas are routine — 13 of 119 live
landing shas are unreachable, inherent to ff-lands recording HEAD-at-record-time
which later amends orphan. No ledger lines were lost.

So this task is about preventing the FAILURE + manual recovery, not repairing
data. Do NOT add a ledger/DB cleanup step for E-1657 — there is nothing to fix,
and editing the append-only WAL for a benign-and-normal artifact would be net
harm. The fix below stops the half-fail that forces the retry in the first place.

## Verify

- A branch that adds a new `task_types` value lands in one `just land` run with
  no manual `just install` in between; the ledger entry records successfully.
- The integrity gate still fires for a genuinely drifted DB (don't weaken it).
