# Plan — E-1929: replace task deletion with a `removed` flag

Implements ED-1547 for tasks. `task remove` stops issuing a DELETE and marks the
row `removed = 1`, so the id can never be re-minted and the FK-free rows that
deliberately outlive their task can never resurrect against unrelated work.

Absorbs E-1930, E-1931 and E-1932 (all closed `obsolete`).

## Decisions this plan implements

Settled with the requester before filing; none of these are open.

| Question | Decision |
|---|---|
| FK actions that stop firing on removal | Split: explicitly null `sessions.active_task_id` and `session_statuses.active_task_id`; leave `task_landings` rows in place |
| `task.bulk_cleared` (`task import --replace`) | Retains too — one rule, no second orphaning path. Whether `task import` earns its keep at all is E-1933 |
| A removed task's visibility | `task show <id>` renders it marked removed; `task list --removed` lists them; invisible everywhere else |
| Scope | Tasks only. Decisions and sessions have no reachable delete path, and decisions are being merged back into tasks |
| Read-filter mechanism | New `live_tasks` view; rewrite the read sites to use it |

## Why the id can be re-minted today

The task-id allocator in `internal/events/executor.go` allocates with
`SELECT COALESCE(MAX(id), 0) + 1 FROM tasks`. Only the *highest* id is ever
re-freed — interior gaps are never refilled — so no historical backfill is
needed. Retaining the row keeps `MAX(id)` monotonic, which is the entire fix.

**The single most important constraint in this change: that allocation query
must keep reading `tasks`, never `live_tasks`.** Pointed at the view it would
stop seeing removed rows, `MAX(id)` would drop back, and ids would be reused
again — reintroducing the exact bug while appearing to fix it. The sibling
decision allocator in the same file has the same hazard; this plan does not
otherwise touch it.

## 1. Schema migration

New change file `internal/schema/changes/e-1929-add-tasks-removed.sql`, applied
at land time by `endless db apply-change`; the runner wraps it in
`BEGIN IMMEDIATE` and records the `_schema_version` marker, so it runs once.

```sql
ALTER TABLE tasks ADD COLUMN removed INTEGER NOT NULL DEFAULT 0;

CREATE VIEW IF NOT EXISTS live_tasks AS
  SELECT * FROM tasks WHERE removed = 0;

CREATE INDEX IF NOT EXISTS idx_tasks_removed ON tasks(removed);
```

Plus the one-shot data repair in §5, in the same transaction.

Per ED-1472 `schema.sql` is the single source of truth and must be edited to
declare the post-migration shape — the `removed` column on `tasks`, the
`live_tasks` view, and the index. The sandbox and tests build from `schema.sql`
and never apply change files, so a change file without the matching `schema.sql`
edit passes the migration and fails every fresh build.

`SELECT *` in the view is deliberate: it inherits future columns automatically.
If that proves fragile against `ALTER TABLE`, the fallback is an explicit column
list, which then has to be maintained alongside `schema.sql`.

## 2. Removal path — executor

`execTaskDeleted` in `internal/events/executor.go`.

Replace both DELETEs with UPDATEs:

- Non-cascade: `UPDATE tasks SET removed = 1 WHERE id = ?`
- Cascade: same `WITH RECURSIVE tree(id)` walk, `UPDATE tasks SET removed = 1
  WHERE id IN (SELECT id FROM tree)`

Keep the existing `UPDATE tasks SET parent_id = NULL WHERE parent_id = ?`. Today
that re-roots the orphans; retaining the row would otherwise leave children
hanging off an invisible parent, so preserving it is what keeps the tree
readable.

Add what the FKs used to do and now will not:

```sql
UPDATE sessions         SET active_task_id = NULL WHERE active_task_id = ?;
UPDATE session_statuses SET active_task_id = NULL WHERE active_task_id = ?;
```

Both were `ON DELETE SET NULL`. A session left pointing at a removed task is
live state that would be a lie, and E-1856 shows `active_task_id` drift is not
hypothetical. Under `--cascade` these must run for every id in the tree, not
just the root.

`task_landings` was `ON DELETE CASCADE` and now simply will not fire. That is
the intended outcome — landing history is audit data, and the retained task row
is there to explain it. No code needed; note it in the change-file comment so a
future reader does not "fix" it.

Delete pending notices in the same path (absorbed E-1931):

```sql
DELETE FROM session_notices WHERE task_id = ? AND notified = 0;
```

Undelivered mail about a removed task is dead; filtering it on read would leave
it queued permanently. Delivered history is untouched. This is not an exception
to ED-1547 — a notice's own id is an internal row handle nobody refers to.

`execTaskBulkCleared` in the same file gets the identical treatment: its
`DELETE FROM tasks WHERE project_id = ? AND source_file = ?` becomes an UPDATE,
keeping its existing `parent_id` re-rooting. E-1915's reasoning applies — one
rule with no exemption beats two rules with a judgment call at the boundary.

The E-1915 relation guard and the E-1927 bulk-clear guard both stay exactly as
they are. Refusing to remove a task that still has relations is orthogonal to
how the removal is then recorded.

## 3. Removal path — projector

The `task.deleted` and `task.bulk_cleared` replay handlers in
`internal/events/projector.go` must be changed to match §2 exactly.

This is the rebuild path: it replays the whole ledger to rebuild the DB from
scratch. If the projector still replayed removal as a real DELETE while the
executor marked `removed = 1`, a rebuild would silently re-free every removed
id — the failure ED-1547 calls out by name, on a path nobody would think to
test. §6 verifies it explicitly.

## 4. Read-path rewrite

Point every read at `live_tasks`. Find the set — do not trust any list written
here, which is why this section states searches rather than locations:

- `FROM tasks` and `JOIN tasks`, across Go, Python and tests. This is the sweep.
- `UPDATE tasks`, `INSERT INTO tasks`, `DELETE FROM tasks` — the complement.
  These are writes and must NOT move; SQLite views are not writable.

The session-status reads are the ones that motivated E-1926: they join `tasks`
and today drop dangling references by accident. Retaining the row makes them
start matching, so they are where a missed rewrite leaks a removed task into
`session status` and the monitor. Find them by the sweep above; they are the
joins feeding the status and monitor renderers.

**Must NOT be rewritten** — each needs a comment saying why:

- The task-id allocator — the `COALESCE(MAX(id), 0) + 1 FROM tasks` query. See
  the boxed constraint above. This is the one that silently undoes the whole
  change, and it is the first thing to grep for.
- Every write, per the complement search above.
- The removal path itself (§2, §3) — it must see and set rows the view hides.
- `task show <id>` and `task list --removed` (§5).
- The §5 repair sweep.
- Anything under the schema `changes/` directory. Historical migrations are
  applied-once history, not live code; editing them rewrites the past.
- `reconcile`'s orphan detection, if its purpose is finding rows whose task is
  gone. Decide per call site while reading it.

## 5. CLI surface

`remove_item` in `src/endless/task_cmd.py` keeps its guards and messaging; only
the emitted effect changes. Reword its output from "deleted" to "removed" so the
CLI stops claiming something it no longer does.

`task show <id>` resolves against `tasks`, not `live_tasks`, and renders a
removed task with a clear marker. This is the payoff for retaining the row: an
id that is a hole explains itself instead of erroring.

`task list --removed` lists removed tasks, querying `tasks WHERE removed = 1`.
Define its interaction with the existing filters — simplest is that `--removed`
replaces the default live set rather than adding to it, so it never interleaves
removed and live rows in one listing.

**One-shot repair, absorbed from E-1932.** In the same migration transaction,
delete `session_tasks` rows that provably belong to a previous occupant of a
reused id — the row's timestamp predates its task's `created_at`:

```sql
DELETE FROM session_tasks
 WHERE id IN (
   SELECT st.id FROM session_tasks st
     JOIN tasks t ON t.id = st.task_id
    WHERE st.created_at < t.created_at
 );
```

Measured against the real ledger while planning, the affected set was small,
confined to session_tasks; the dangling rows whose task no longer exists are harmless — they are what "outlives its task" means, and are already
invisible through the view. Re-measure all four tables before running the sweep
rather than assuming that shape held.

## 6. Verification

A bespoke `tests/tasks/e-1929-verify.sh`, following the shape the prior verify
scripts use: a throwaway git repo as project root under a temp dir, with its own
`XDG_CONFIG_HOME` (hence its own DB and ledger) and `XDG_CACHE_HOME`, the
worktree's `bin/` prepended to `PATH` so the event bridge execs the candidate
binary, pass/fail per check with a summary, and exit 0 / 1 / 2 for
all-passed / any-failure / setup problem.

`verify.toml` is deliberately not used: that functionality has not landed.

The script must assert:

1. Remove a task — the row survives with `removed = 1`.
2. It is absent from `task list`, `session status` and the monitor.
3. `task show <id>` renders it marked removed.
4. `task list --removed` lists it.
5. **Id monotonicity.** Remove the highest task, add a new one, assert the new
   id is greater than the removed one. This is the regression the whole change
   exists to prevent.
6. `sessions.active_task_id` and `session_statuses.active_task_id` are nulled;
   `task_landings` rows survive.
7. Pending `session_notices` for the removed task are gone; delivered ones
   remain.
8. `task remove --cascade` marks every descendant and nulls every descendant's
   `active_task_id` pointers.
9. The E-1915 relation guard and E-1927 bulk-clear guard still refuse.
**Executor/projector parity — do NOT verify this by rebuilding a real DB.**

The hazard is real: if the projector still replays removal as a DELETE while the
executor marks `removed = 1`, a rebuild re-frees every removed id and the change
ships looking complete. But `rebuild-db` is not yet reliable, and the main DB is
canonical until it is (see the rebuild-reliability epic). A verify script that
rebuilds is both a risk to real data and a source of flaky failures attributable
to the rebuild rather than to this change.

Verify it instead with a Go test in the events package, following the existing
`TestProjectToTempDB_*` pattern: project a synthetic event stream —
`task.created`, then `task.deleted`, then a fresh `task.created` — into a temp
DB, and assert the removed row is present with `removed = 1` and that the new
task's id is greater than the removed one. That exercises the projector's
removal handler and the allocator against replayed state, in isolation, with no
real DB and no dependence on `rebuild-db` working end to end.

Add the same assertions for `task.bulk_cleared`.

Plus `just build && just test`.
