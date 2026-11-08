# Plan

Every `event emit` apply runs apply-first in one write transaction: `BEGIN IMMEDIATE`, run the SQL, append the primary ledger line and any `epic.status_derived` lines, `COMMIT`, then commit the segment to git. A SQL failure rolls back and nothing reaches the ledger. The only window left is a COMMIT that fails after the append; emit then writes a `ledger.voided` line naming the run's segment and byte offsets and the error, commits the segment, and tells the user the ledger holds the entry and its voiding.

`ledger.voided` is a new kind, system-written. The projector pre-scans for it and skips every line it names. `endless-go event void` writes one by hand for a run emit could not void; it is user-only, takes the segment as its one positional, and requires `--reason`. It voids the E-2486 `task.landed` line that never applied.

The id-allocating create path already holds `BEGIN IMMEDIATE` across the append; it moves its append after the INSERT so the same ordering holds everywhere.

Decided without asking: `event void` is Go-only with no Python command; a void's actor is the voided event's actor; the void entry names its segment explicitly because the run and its voiding can straddle a rotation.

Answered in chat on 2026-11-08: Q1 apply-first in one transaction plus the voiding entry (C); Q2 no retry loop (A); Q3 a new kind `ledger.voided` (B); Q4 void the E-2486 line (A); Q5 no crash detection, left to E-914 (A).
