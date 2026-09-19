# Plan — removal retains the tree edge; renders adopt the nearest live ancestor

## What happens today

ED-1547 (E-1929) replaced deletion with `removed = 1` so an id is never
re-minted. The row survives; the tree edge does not. `removeTaskTree` (shared by
the executor and the projector) runs `UPDATE tasks SET parent_id = NULL WHERE
parent_id = ?` on the non-cascade path, and `removeTasksBySourceFile` does the
same in bulk for `task import --replace`. Both mirror the `ON DELETE SET NULL`
the hard-delete path used.

For a LIVE child that is load-bearing today: every tree read joins `live_tasks`
to `live_tasks`, so a live child left pointing at a removed parent would hang off
a row the view hides and disappear from `task list`, `task tree` and the
monitors.

For a child that is ITSELF removed it protects no render at all and simply
destroys the association. There is no restore verb, so nothing in the live
database can reconstruct it. Cascade removal is unaffected — it flags the subtree
and leaves every edge intact.

## Decisions (Mike, 2026-09-18 — settled)

1. **Stop nulling; keep `parent_id`.** Both statements go.
2. **Renders adopt the nearest live ancestor.** A live child of a removed parent
   renders under the closest ancestor that is not removed, so it appears under
   the grandparent rather than vanishing or jumping to top level, and the
   original edge survives for history and for any future restore.
3. **Backfill from the ledger.** Edges already nulled are recovered by replaying
   what the ledger still carries.

## Build

1. **Removal keeps the edge.** Delete the two `parent_id = NULL` updates in
   `internal/events/task_removal.go`. Both the executor and the projector call
   this one function, so the live path and replay stay identical by construction
   — the property that file exists to protect.
2. **One place answers "who is my effective parent".** Add it beside
   `live_tasks` in the schema as a view (e.g. `task_tree`) exposing
   `effective_parent_id`: the first ancestor with `removed = 0`, walked with a
   recursive CTE, and NULL when every ancestor is removed. A view rather than a
   computed column, and one view rather than a rule repeated at each reader —
   the same argument `live_tasks` itself is built on, and the tables are small
   enough that the walk costs nothing.
3. **Point tree reads at it.** `parent_id` appears ~101 times in `task_cmd.py`
   and ~23 times across `internal/monitor` and `internal/events`. Most are
   writes, validation or single-row lookups and must keep using `parent_id`;
   what changes is only the reads that BUILD A TREE or count children — the
   recursive `JOIN tree ON t.parent_id = tree.id` descent queries, the
   leaf/childless-count predicates, `task list`/`task tree` rendering, the epic
   roll-ups in `epic_derivation.go`, and the monitor's tree reads. Convert those
   to `effective_parent_id`; leave every other use alone. The distinction to hold
   while converting: `parent_id` answers "what did the user set", and
   `effective_parent_id` answers "where does this render".
4. **The guard that made this invisible stays.** `task remove` still refuses a
   non-cascade removal whose children are live; nothing here loosens it. The
   nulling was reachable for children that are already removed and through the
   bulk clear, which is precisely the destructive half.
5. **Backfill, as a schema change** under `internal/schema/changes/` (the `.go`
   form, following the recent precedent there). For every task whose `parent_id`
   is NULL today, read the ledger for the last event that set its parent; if that
   event names a parent and the parent row still exists, restore it.
   The nulling is a side effect that emits NO event of its own, so the last
   parent-bearing event in the ledger is authoritative — which also means a task
   deliberately re-rooted with `task move <id> --root` has a final event saying
   NULL and is correctly left alone. Report counts: restored, skipped as
   deliberate roots, skipped because the parent row is gone.
6. **Rebuild parity.** A rebuild from an old ledger now replays removal without
   nulling, so rebuilt trees keep edges the live database lost. That is the
   intended direction, and it makes step 5's result and a rebuild agree rather
   than drift.

## Verification

- Remove a task whose only children are themselves removed; `parent_id` on those
  children is unchanged, and `task list --removed` still shows them under it.
- `task import --replace` over a source file whose tasks have children: the live
  children keep `parent_id` and render under the nearest live ancestor instead of
  jumping to the root.
- A live child of a removed parent appears under its grandparent in `task list`
  and in the monitor, and does not disappear.
- The backfill reports a non-zero restored count on the real database, leaves
  every `task move --root` task at NULL, and is idempotent on a second run.
- `just test` and `just test-go` pass; `validate-db` reports no new drift between
  the live database and a ledger replay.
