ED-1547 says no id that anything outside its own row refers to is ever reused.
`tasks` got the treatment in E-1929 (a `removed` column, a `live_tasks` view, an
audited read path); `decisions` gets it in E-2010. `sessions` is the remainder,
and it was deliberately left out of E-2010 rather than forgotten — the reasoning
is below so this task is not re-argued from scratch.

WHY IT WAS DEFERRED, NOT DROPPED

Nothing removes a session today: `git grep "DELETE FROM sessions"` over
internal/ and src/ returns nothing. So this is pre-work — there is no live bug
to fix and no data at risk right now. What it costs is not small: 82 non-test
read sites across 36 files reference `sessions` by FROM or JOIN, and every one
has to be classified as "enumeration, use the view" or "this row specifically,
use the table". Folding that into E-2010 would have buried a live project
data-loss fix (the disk-gone sweep that hard-deleted a project row and cascaded
its tasks and activity away) under the largest read-path audit of the four
entities, for the one entity that cannot yet lose anything.

WHY IT IS STILL REAL

`sessions.id` is declared `id INTEGER PRIMARY KEY` with no AUTOINCREMENT, which
makes it a rowid alias. SQLite's default allocator is max(rowid)+1, so deleting
the highest session row hands that exact id to the next session created — the
same re-mint hazard the explicit `MAX(id) + 1` allocators carry for tasks and
decisions, just supplied by SQLite instead of by our own SQL. An ES-NNNN id is
written into session_tasks, session_notices, session_statuses, activity, hidden
and pinned task rows, and into task_show output a human reads, so a reused id
silently repoints all of it.

WHAT WILL REACH IT FIRST

E-2486's `endless correct <target> delete` is the first path that can remove a
session. Its plan already reads the right way round — it marks `removed = 1` on
any target whose table has a `removed` column and DELETEs elsewhere — so it is
correct either way and does NOT have to wait for this task. Until this lands,
though, `correct session delete` is a hard delete, mitigated only by its
referrer guard (it refuses when session_tasks, session_notices or hidden/pinned
rows point at the session). A session with no referrers is deletable, and
deleting the highest one re-mints its id.

ORDERING

Preceded by E-2010 for a mechanical reason, not a conceptual one: both tasks add
a goose migration under internal/schema/migrations/, and worked in parallel they
would collide on the same version number. E-2010 also establishes the pattern
(column declared LAST so ALTER TABLE's append position matches schema.sql for
rebuild-db, view body only — never an index, CHECK or generated column naming
`removed`, since those resolve eagerly at CREATE time and would abort a
migration against a DB predating the column).

Deliberately NOT blocking E-2486, and not preceding it either: E-2486 is this
work's only consumer, it degrades correctly without it, and a `precedes` edge
would re-impose the claim refusal that E-2010's land is supposed to clear.
