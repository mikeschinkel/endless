Landing E-2486 on 2026-11-08, `event emit --kind task.landed` wrote the ledger line, committed it (596574b51 on main), and then failed to apply it:

    endless-go event: error: events: insert task_landing: database is locked (517)

517 is SQLITE_BUSY_SNAPSHOT: a deferred transaction had read, another connection committed in between, and the first write then fails at once. busy_timeout (5000 ms, WAL) does not retry it. `events.Execute` opens its transaction with a plain `db.Begin()`; the id-allocating paths (`PreAllocateTaskID`, plan items, questions, corrections) already use `BEGIN IMMEDIATE` and are not exposed.

Result: the ledger says E-2486 landed and the database has no task_landings row for it. `event rebuild-db` is refused (E-2062), so nothing replays the line; E-914 is the owner of case-by-case repair and holds this entry as evidence.

Mike asked: "Can we make this robust? If not, can we make it be able to do a rollback (or write a reversing entry?)"

Both, in order:

1. Robust first. `Execute` takes `BEGIN IMMEDIATE`, and retries the whole apply a bounded number of times on SQLITE_BUSY / SQLITE_BUSY_SNAPSHOT before reporting. That removes the ordinary cause.
2. A reversing entry when the apply still fails. The ledger is written first on purpose (events-authoritative), so a failed apply must not be silently left behind. On an apply failure after the append, write an entry (shape to decide: a `ledger.corrected` annotation pointing at the failed line's byte offset, as E-2486's correction entries do, or a dedicated kind) that says this line never took effect, so a replay skips it and a reader sees why; then report the command as not recorded. The command the user ran can then simply be re-run.
3. The one unapplied `task.landed` for E-2486 already on the ledger needs either a reversing entry of the same shape or E-914's repair; decide which when (2) exists.

Not this task: the land's status handling after a failed record (see the brainstorm on lifecycle demands) and busy_timeout before the first read (E-2404).
