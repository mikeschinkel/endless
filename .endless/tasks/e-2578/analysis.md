# Analysis

**Why the order changes.** E-808 (c9425fb7f) reordered emit from apply-first to ledger-first so that a failed apply would be "picked up by rebuild-db". E-2062 made rebuild of the live database refuse, and E-808's outcome records that disaster-recovery rebuild is false by design. So a failed apply now leaves a ledger line nothing will ever apply, which is the drift seen on 2026-11-08: `task.landed` for E-2486 on the ledger (596574b51), no `task_landings` row. Apply-first in one write transaction removes the ordinary cause: a SQL failure writes nothing anywhere. The invariant ED-1024 and ED-1525 call events-authoritative keeps its substance, no committed database change without its ledger line, because the append precedes COMMIT in the same transaction. What changes is E-808's letter, "write the event first"; a decision restates the invariant so the two agree.

**Why BEGIN IMMEDIATE and no retry.** Error 517 (SQLITE_BUSY_SNAPSHOT) arises only when a deferred transaction has read and another connection commits before it writes; busy_timeout does not retry it. Taking the write lock up front removes that case, and the existing 5 s busy_timeout is the wait for a lock held by another writer. The description's "retried under a write lock" is met by that wait, not by a retry loop: a loop would triple the latency of an emit that will fail anyway and hide a lock holder worth finding.

**Why a voiding entry remains.** B leaves one window: append succeeded, COMMIT failed. The ledger is then ahead of the database and must say so, which is what Mike asked for ("write a reversing entry"). A new kind rather than `ledger.corrected` because that kind is user-only, is a replay no-op, and records a user's reason; giving it a system author and a replay effect would split one kind into two behaviours. Offsets rather than `ts` because `ts` is not unique in this ledger.

**Why the derived lines are inside the transaction.** They are products of the failed apply; if the primary line never took effect neither did they, so a void covers the whole run.

**Why the create path changes too.** It already holds the write lock across the append but appends before the INSERT, so a trigger or constraint failure there orphans a line in the same way. Moving the append after the SQL makes one ordering hold everywhere.

**Why no crash detection.** Finding a run orphaned by a crash needs a last-applied marker per segment written on every emit and read by the next; that is repair tooling, E-914's area. This task reverses the failure the process sees.

**Why the E-2486 line is voided, not re-applied.** The landing it records failed at its record step and was superseded by the land at d898ca9; voiding makes the ledger say so, re-applying would record a landing that did not take effect.
