## Evidence (measured 2026-08-30)

Scanning all 57 `.endless/db-ledger/*.jsonl` segments by `entity.type == "task"`
and `entity.id`:

- **E-1361** — 6 events. `task.created`, four `task.fields_updated`, the last of
  which sets `status: completed` and an outcome. **No `task.deleted` anywhere.**
  Final title: "Decide that verbs.json and plan snapshots are stored in
  `.git/info/endless/`; project main only receives them at worktree land."
- **E-1279** — 10 events, alternating create/delete four times. Last event is
  `task.deleted`. Title "Enable testing with this task", empty description.
  Correctly absent from the projection.
- **E-1293** — 3 events, last is `task.deleted`. Title "Test spawn flow (delete
  me)", description "scratch". Correctly absent.

`select id from tasks where id in (1279,1293,1361)` returns nothing — E-1361 is
absent, not `removed=1`.

## Why this is a defect and not a deletion

The ledger is durable state; SQLite is a projection rebuildable from it. An
entity with a complete event history and no deletion event must appear in the
projection. Something dropped E-1361 between the two — either a rebuild that
lost it or a projector path that mishandles this event sequence. Which one is
the investigation.

## Scope

1. Find the cause. The event sequence is unremarkable, so start by replaying
   these six events into a scratch DB and seeing whether the projector produces
   the row.
2. Audit the same direction across all entity types — ledger entities with no
   deletion event and no projected row. E-1710 audited the *opposite* direction
   (DB rows missing their originating event) and found no loss; this direction
   was never covered, so the population size is unknown.
3. Restore E-1361 as a consequence of the fix, not as a hand-written row.

Do not restore E-1279 or E-1293.

## From the description

Find the cause, audit for other entities lost the same way, and restore E-1361 as a consequence of the fix.
