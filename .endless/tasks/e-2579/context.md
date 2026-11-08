`endless correct` (E-2486) finds the references between rows two ways. SQLite
reports the ones the schema declares with a FOREIGN KEY clause, through
PRAGMA foreign_key_list. The rest — references the schema holds as a bare
integer column with no FOREIGN KEY — are invisible to the database, so they are
listed by hand in `undeclaredRefs` in internal/events/correction.go.

Two checks read that list, and both fail quietly for a reference missing from
it:

  - the delete refusal in `referrers` counts the rows that refer to the row
    being deleted, and refuses the delete when any exist;
  - the dangling-id check in `checkInvariants` verifies that a value written into
    a referencing column names a real row in the referenced table.

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

WHAT EACH ONE COSTS TODAY

The dangling-id check does not cover them, so
`endless correct error_triage <key> --set accepted_session_id=99999` is accepted
and writes an id naming no session. That is the half with no workaround: a
correcting update is exactly where a mistyped id comes from, and the check
exists to catch it.

The delete refusal does not cover them either, so `endless correct session <id>
delete` can delete a session that one of those rows still refers to. That half
is narrower — `sessions.id` is `INTEGER PRIMARY KEY` with no AUTOINCREMENT, so
it is a rowid alias allocated as max(rowid) + 1, and deleting the highest
session hands that exact id to the next session created. The error_triage row
then reads as "accepted by session N" and points at a real, unrelated session
rather than at nothing, with nothing recording that it is wrong.

WHY THE LIST DRIFTED

Nothing holds `undeclaredRefs` against the schema, so a new column that refers
to a tracked row joins neither check and nothing says so. E-2010 closed the same
hole for the projects guard list with
TestProjectReferences_CoversEverySchemaReference, which reads the foreign keys
out of the live schema and fails in both directions; `undeclaredRefs` has no
equivalent.

RELATED, AND DELIBERATELY NOT PART OF THIS

E-2570 proposed giving sessions a `removed` column and a `live_sessions` view.
It was declined: sessions are not ledgered, nothing in the tree deletes one, and
an abandoned session has no referrers by construction, so the guard already
covers the case ED-1547 cares about — once its list is complete, which is this
task.

Whether `session` should be a delete target at all is a separate question. Its
stated reason is uniformity across all 19 targets (ED-1652) rather than a
demonstrated use case, and narrowing the targets would remove the delete half of
this bug. It would not touch the write half, so this task is needed either way
and does not wait on that question.
