`endless correct` (E-2486) finds the references between rows two ways. SQLite
reports the ones the schema declares with a FOREIGN KEY clause, through
PRAGMA foreign_key_list. The rest — references the schema holds as a bare
integer column with no FOREIGN KEY — are listed by hand in `undeclaredRefs` in
internal/events/correction.go.

Two checks read that list. The delete refusal, in `referrers`, counts the rows
that refer to the row being deleted; it reads the list by REFERENCED table, so
the referring table does not have to be a correction target. The dangling-id
check, in `checkInvariants`, verifies that a value written into a referencing
column names a real row; it reads the list by REFERRING table, so it only fires
on a target's own correctable columns.

THE LIST IS INCOMPLETE, AND NOTHING SAYS SO

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
at sessions, and checking each remainder against the list. Three arrived
together when error_triage grew its accept and decline columns.

That is the defect: a hand-maintained map of the schema's undeclared references
with nothing holding it against the schema. E-2010 closed the same hole for the
projects guard list with TestProjectReferences_CoversEverySchemaReference, which
reads the foreign keys out of the live schema and fails in both directions.
`undeclaredRefs` has no equivalent, so six columns drifted off it unnoticed and
the next ones will too.

WHAT THE SIX COST TODAY, AFTER ED-1655

Before ED-1655 the missing entries were reachable: `endless correct session <id>
delete` counted no referrer for them, so it deleted a session that an
error_triage, errors, errors_sources or session_notices row still named — and
because `sessions.id` is a rowid alias allocated as max(rowid) + 1, deleting the
highest session handed that id to the next one created, leaving those rows
naming a real but unrelated session while reading as correct.

ED-1655 removes that reachability by deciding sessions are never deleted, which
this task implements. So the six entries guard nothing once the delete is gone.
They are still required, because the invariant is that the list maps every
undeclared reference the schema holds: which of the two checks reads a given row
is incidental, and a list kept only where it is currently load-bearing is the
list that just drifted.

The dangling-id check never covered the six and still will not. errors,
errors_sources and error_triage are not correction targets at all — internal/faults
writes them directly and nothing about them goes through the ledger.
session_notices is a target, but its correctable column is `notified`, and
`changed_by_session` is nullable, so it is neither in the target's column list
nor among the table's required columns and an insert cannot supply it either.

A COMMENT THAT OVERSTATES ITS CASE

E-2010 left a comment in internal/events/projector.go saying a DELETE on the
replay path would re-free every removed id "on a rebuild". Rebuild of the LIVE
database is refused (E-2062), so that reads as though a routine operation were
at stake. The hazard is real for temp-database projection, which is what the
verification path uses, and for any future rebuild — the wording is what is
wrong, not the reasoning.
