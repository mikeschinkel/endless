## What is actually there

Verified 2026-09-14, not estimated.

**The writer ships; the reader was never built.** `endless task next revise`
("Replace the curated 'next' list from a JSON file") exists and works.
`endless project next` — the command that would read the list — does not exist;
invoking it errors with "No such command". `endless session next` does not exist
either. So the feature is a populated store nothing displays.

**Backing to remove:**

- Command: `task next revise` (a subcommand of `task next`, which itself stays —
  `task next` is the live "what should I do next" query and is unrelated).
- Tables: `project_next`, `project_next_lanes`, `project_next_tasks`,
  `project_next_pending`, `project_next_events`.
- Go: `internal/events/project_next.go`,
  `internal/events/project_next_pending.go`, and the project-next branches in
  `internal/events/event.go`, `internal/events/executor.go`,
  `internal/eventcmd/event.go`, `internal/monitor/project_path.go`.
- Python: the project-next reads in `src/endless/task_cmd.py`.

**Already closed as part of this:** E-1436, E-1438, E-1439, E-1440 and E-1485
declined 2026-09-14; E-1441 and E-1442 were obsoleted earlier on the same
grounds.

## The ledger question this has to answer

`project_next_events` and the event handlers mean the db-ledger may hold
project-next events. Dropping the tables is easy; the projector must still not
choke on historical events it no longer has a home for. Decide explicitly
whether the projector ignores them or whether they are treated as a replayable
no-op — and note that rebuild is currently broken and on hold until after E-894,
so this cannot be validated by running a rebuild.

This is the one part that is not a straight deletion.

## `task import`

Separate and much smaller. Mike decided 2026-09-14 that it should be removed;
E-1933, which existed to decide that, is declined. Verify nothing else invokes it
before deleting — it is plausible that a setup or migration path shells it.

## Session task ordering — remove the ordering, keep the tree

Mike, 2026-09-14: `session status --tree` stays as a command; its ORDERING
aspect goes. He may revisit what `--tree` should become later.

Remove:

- `endless session order` — the only writer of `session_tasks.do_order`.
- The `session_tasks.do_order` column.
- `monitor.SessionStatusDoOrder` and its query.
- The override path in `internal/sessionstatuscmd/tree.go`: `buildForest`'s
  `doOrder` branch and `assignByLayer`, so the tree always derives its order from
  the DAG rather than from explicit layers.
- The do_order handling in `internal/events/session_tasks.go` and the membership
  carry-over in `internal/events/session_task_membership.go`.

Keep `--tree` itself. After this it renders the DAG-derived order only, which is
what it does today whenever no `do_order` rows exist — so the common case is
already the surviving behaviour and this narrows the feature rather than
changing it.

Verified before scoping: `SessionStatusDoOrder` is called from `tree.go` and
nowhere else, and the flat `session status` board never reads `do_order`. So the
removal cannot affect any surface other than the tree's layering.

Same ledger caveat as the curated-next removal: session-order events may exist in
the db-ledger, and the projector must be decided explicitly rather than left to
fail on replay.

## Explicitly NOT in scope

`session status --tree` as a command. It survives; only its explicit-ordering
override is removed. What `--tree` should ultimately become — whether it needs
blocks and relations to earn its keep — is a separate product question Mike has
deferred.
