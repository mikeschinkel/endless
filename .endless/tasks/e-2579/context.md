`endless correct` (E-2486) finds the references between rows two ways. SQLite
reports the ones the schema declares with a FOREIGN KEY clause, through
PRAGMA foreign_key_list. The rest — references the schema holds as a bare
integer column with no FOREIGN KEY — are invisible to the database, so they are
listed by hand in `undeclaredRefs` in internal/events/correction.go.

Two checks read that list. The delete refusal, in `referrers`, counts the rows
that refer to the row being deleted and refuses the delete when any exist; it
reads the list by REFERENCED table, so the referring table does not have to be
a correction target. The dangling-id check, in `checkInvariants`, verifies that
a value written into a referencing column names a real row; it reads the list by
REFERRING table, so it only ever fires on a target's own correctable columns.

THE SIX MISSING ENTRIES

Fifteen INTEGER columns outside `sessions` refer to a sessions row without a
declared foreign key. Nine are in `undeclaredRefs`. Six are not:

    error_triage.session_id
    error_triage.accepted_session_id
    error_triage.declined_session_id
    errors.session_id
    errors_sources.session_id
    session_notices.changed_by_session

Found by querying the live schema for every INTEGER column whose name mentions
session, subtracting those PRAGMA foreign_key_list already reports as pointing
at sessions, and checking each remainder against the list. Three of the six
arrived together when error_triage grew its accept/decline columns, which is the
drift this task is really about.

WHAT THIS COSTS, AND WHAT IT DOES NOT

Only the delete refusal is affected. `endless correct session <id> delete`
counts no referrer for any of the six, so it deletes a session that an
error_triage, errors, errors_sources or session_notices row still names. Those
rows then hold an id for a session that no longer exists.

It does not stop there. `sessions.id` is `INTEGER PRIMARY KEY` with no
AUTOINCREMENT, which makes it a rowid alias allocated as max(rowid) + 1, so
deleting the highest session hands that exact id to the next session created.
The error_triage row then reads as "accepted by session N" and points at a real
but unrelated session rather than at nothing, with nothing recording that it is
wrong. A dangling id is findable; a plausible wrong one is not.

The dangling-id check is NOT affected, and that is worth stating because it is
easy to assume otherwise. errors, errors_sources and error_triage are not
correction targets at all — they are written directly by internal/faults, not
through the ledger — so no correcting write can reach their columns.
session_notices IS a target, but its correctable column is `notified`, and
`changed_by_session` is nullable, so it is neither in the target's column list
nor in the table's required columns and an insert cannot supply it either. All
six are unreachable by a correcting write today.

WHY THE LIST DRIFTED

Nothing holds `undeclaredRefs` against the schema, so a new column that refers
to a tracked row joins neither check and nothing says so. E-2010 closed the same
hole for the projects guard list with
TestProjectReferences_CoversEverySchemaReference, which reads the foreign keys
out of the live schema and fails in both directions; `undeclaredRefs` has no
equivalent. That test, not the six entries, is what keeps this from recurring.

RELATED, AND DELIBERATELY NOT PART OF THIS

E-2570 proposed giving sessions a `removed` column and a `live_sessions` view.
It was declined: sessions are not ledgered, nothing in the tree deletes one, and
an abandoned session has no referrers by construction, so the guard already
covers the case ED-1547 cares about — once its list is complete, which is this
task.

Whether `session` should be a delete target at all is a separate question. Its
stated reason is uniformity across all 19 targets (ED-1652) rather than a
demonstrated use case, and dropping the delete would remove this bug's only
reachable harm rather than guard against it. That question is open; this task
assumes the command stays.
