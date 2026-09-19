Absorbs E-1930 (extend the policy to non-task entities) and E-1931 (drop pending
notices), both closed obsolete in favour of this task.

## Scope: which entities are covered

ED-1547 covers any id that something outside its own row refers to — logically,
not only where SQLite declares a FOREIGN KEY. That is tasks, sessions and
decisions. A junction or child table whose surrogate primary key nothing else
stores and nobody types stays an unrestricted hard delete.

## Three parts

**1. Convert each covered entity's delete path.** Set a new `removed` flag on the
row rather than issuing a DELETE. Sessions are the known live exposure:
`session_tasks.session_id` refers to a session with no declared constraint,
exactly as `task_id` does, and `task_cmd.py` already renders a state for a touch
whose session row is gone — useful precedent for the mirror case.

**2. The read-path audit — the bulk of the work.** Queries that today drop
dangling references via inner join will start matching the retained row, so a
removed entity leaks into listings unless each read filters on `removed`. Known
sites include `session_status.go` in internal monitor at lines 184, 349 and 480.
The full sweep covers CLI listings, session status, the monitor, `session next`,
the web dashboard, and the four consumers `session_tasks`,
`session_hidden_tasks`, `session_notices` and `project_next_tasks`/`_pending`.

**3. Drop pending notices in the same removal path.** Delete `session_notices`
rows with `notified = 0` for the removed task rather than filtering them —
an undelivered notice about a removed task is dead mail that would otherwise sit
in the queue permanently. Delivered notice history is unaffected. This is not an
exception to the id rule: a notice's own id is an internal row handle nobody
holds.

## Rebuild check

Confirm the db-ledger projector replays a removal as a retained `removed=1` row.
If it ever replayed removal as a real delete, a rebuild would re-free the id and
reuse would return on a path nobody would think to test.
