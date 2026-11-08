Three changes in internal/events, all small, plus the test that is the point of
the task.

SESSION STOPS BEING A DELETE TARGET (ED-1655)

`correctionTargets` in internal/events/correction_targets.go is a closed list,
and `delete` is not gated per target today — every target with a table accepts
it. Add a per-target flag (a `NoDelete bool` on CorrectionTarget, or equivalent)
and set it on `session`, so a delete is refused naming the decision while
`update`, `insert` and `annotate` on a session keep working: `state`, `task_id`,
`focus_task_id` and `hidden` are all legitimately correctable, so the target
itself stays.

Refuse it in the same shape as the command's other refusals, naming why rather
than reporting a missing feature — a user who reaches for it should learn that
sessions are not deleted, not that the command is incomplete.

THE SIX ENTRIES

Add one `undeclaredRef` row per missing column to `undeclaredRefs`, each with
RefTable "sessions" and RefColumn "id", beside the nine session rows already
there. `Where` stays empty: none is conditional. Keep them grouped by table so
the list still reads table by table.

Comment the group with what it is for, because after ED-1655 these rows have no
live consumer and the next reader will wonder: the list maps the schema's
undeclared references, the test below enforces that, and no current check reads
these six.

THE TEST

Add a test to internal/events/correction_test.go that derives the expectation
from the schema instead of restating it, in the shape of
TestProjectReferences_CoversEverySchemaReference in internal/monitor:

  - build a schema-only database;
  - join sqlite_master to pragma_table_info for every INTEGER column whose name
    names a table the corrections reference, excluding that table's own row;
  - subtract every reference PRAGMA foreign_key_list already declares;
  - fail naming any remainder absent from undeclaredRefs, and fail in the other
    direction for a row in undeclaredRefs whose table or column no longer
    exists.

Derive it from pragma, not from parsed SQL text. A manual pass over schema.sql
got this wrong in both directions — it invented a jobs.session_id that does not
exist and missed errors.session_id and errors_sources.session_id — because a
table altered by ADD COLUMN ends with `, col TYPE);` on its closing line, which
defeats a regex anchored on a newline before the paren.

State the column-matching rule in a comment. The test is only as good as what it
recognizes as a reference, and the naming convention is the weak part: a column
holding a sessions id without "session" in its name escapes both the list and
the test. Say that is the known limit rather than let a reader take the test for
total.

THE PROJECTOR COMMENT

Reword the "on a rebuild" sentence in internal/events/projector.go so it names
temp-database projection, which is what actually exercises the path, and a
future rebuild rather than implying the live one. One sentence; the reasoning
around it stands.

SCOPE

No schema change and no migration. Sessions gain no `removed` column and no
`live_sessions` view (E-2570, declined; ED-1655). `sessions.id` is not rebuilt
with AUTOINCREMENT. No other target's operations change.
