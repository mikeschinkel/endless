Observed 2026-08-25 on `task show E-799 --children`. The Children table's Status column is sized by 'obsolete (replaced by E-1367)' — about 29 characters against 8-10 for a bare status — so the Title column is truncated on every row to pay for four annotated ones:

    E-810   next   unplanned                          Migrate task ordering to Lexorank strings
    E-815   next   obsolete (replaced by E-814)       Implement conflict surfacing and resolutio…
    E-893   next   obsolete (replaced by E-1367)      Consolidate Go binaries into unified endle…
    E-894   now    ready                              Move task display reads from Python to Go

The annotation is right where it was asked for. A detail view has one status and
unlimited width; a table has many rows and a shared column, so the same string
costs every row. That asymmetry is the whole defect — this is not a request to
undo E-1956.

Worth checking while in there, as evidence rather than as scope: the sibling
annotation for `duplicates` (E-1185) and `internal/sessionstatuscmd/session_status.go:885`,
which comments that it follows the same shape, so `session status` may render a
status column with the same problem.