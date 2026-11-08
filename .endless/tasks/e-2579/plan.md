Two changes in internal/events: the six entries, and the test that keeps the
list honest. The test is the larger half and the reason the task is worth
filing.

THE SIX ENTRIES

Add one `undeclaredRef` row per missing column to `undeclaredRefs` in
internal/events/correction.go, each with RefTable "sessions" and RefColumn "id",
beside the nine session rows already there. `Where` stays empty: none of the six
is conditional. Keep them grouped with their table's other rows so the list
still reads table by table.

Two things belong in a comment on the group. First, error_triage carries three
of them and they arrived after the list was written, which is why the drift was
invisible. Second, these six feed the delete refusal only — their tables are
either not correction targets or do not expose the column for correction — so a
later reader does not go looking for the dangling-id behaviour and conclude the
entries are dead.

THE TEST

Add a test to internal/events/correction_test.go that derives the expectation
from the schema instead of restating it, in the shape of
TestProjectReferences_CoversEverySchemaReference in internal/monitor:

  - build a schema-only database;
  - join sqlite_master to pragma_table_info for every INTEGER column whose name
    names a table the corrections reference, excluding the referenced table's
    own row;
  - subtract every reference PRAGMA foreign_key_list already declares;
  - fail naming any remainder absent from undeclaredRefs, and fail in the other
    direction too for a row in undeclaredRefs whose table or column no longer
    exists.

Derive it from pragma rather than from parsed SQL text. The earlier manual pass
over schema.sql got this wrong in both directions — it invented a
jobs.session_id that does not exist and missed errors.session_id and
errors_sources.session_id — because a table altered by ADD COLUMN ends with
`, col TYPE);` on its closing line, which defeats a regex anchored on a newline
before the paren.

Name the column-matching rule in a comment: the test is only as good as what it
recognizes as a reference, and a naming convention is the weak part. A column
that refers to a sessions row without "session" in its name would escape both
the list and the test, so the comment should say that is the known limit rather
than let a later reader assume the test is total.

SCOPE

No schema change, no migration, no new column on sessions (E-2570, declined). No
change to which targets `correct` accepts, and no attempt to bring these six
within reach of the dangling-id check.
