# Populate sessions.active_epic_id on interactive task claim

## Context
E-1571 added `sessions.active_epic_id` and specified that a coordinator
(interactive/fg) session working under an epic should carry the epic's id in
`active_epic_id`, with `active_task_id` tracking the viewed child. It explicitly
deferred the *write* for interactive sessions out of its own scope ("part of
E-1570 and the day-to-day `endless task claim` flow"). That write was never
implemented.

Today `active_epic_id` is set **only** for background-agent rows
(`RecordBgAgentSession`, internal/monitor/session.go:341). The claim handler
`execTaskClaimed` (internal/events/executor.go:752) sets only `active_task_id`.
Consequences:
- The E-1571 status-line `[E-EEEE:E-CCCC]` / `[E-EEEE]` epic prefix can never
  render for interactive sessions (it branches on `active_epic_id`, always NULL).
- `endless agents` (E-1621) auto-resolves the active epic from the caller's
  `active_epic_id`, so the interactive auto path always errors until this lands.

## Change
In `execTaskClaimed` (internal/events/executor.go), when updating the session on
claim, also set `active_epic_id = nearestEpicAncestor(taskID)`:
- task has an epic ancestor → that epic id (an epic claimed directly resolves to
  itself, per `nearestEpicAncestor` depth-0 inclusion).
- no epic ancestor → NULL (clears any stale value from a prior claim).

`nearestEpicAncestor` (internal/monitor/session.go:411) is currently unexported
in `monitor`. Either export it (e.g. `NearestEpicAncestor`) for the events
package, or inline the same recursive-CTE resolution in the executor's UPDATE as
a correlated subquery:

```sql
UPDATE sessions
   SET active_task_id = ?,
       active_epic_id = (
         WITH RECURSIVE ancestry(id, parent_id, type_id, depth) AS (
           SELECT id, parent_id, type_id, 0 FROM tasks WHERE id = ?
           UNION ALL
           SELECT t.id, t.parent_id, t.type_id, a.depth+1
             FROM tasks t JOIN ancestry a ON t.id = a.parent_id
         )
         SELECT a.id FROM ancestry a
           JOIN task_types tt ON tt.id = a.type_id
          WHERE tt.slug = 'epic'
          ORDER BY a.depth LIMIT 1
       )
 WHERE id = ?
```

The single-statement subquery avoids exporting the helper and keeps the write
atomic. Pick whichever fits the executor's existing style (it uses raw SQL via
`db.Exec`).

Also handle release: `execTaskReleased` (executor.go:797) NULLs `active_task_id`;
it should NULL `active_epic_id` too, so a released coordinator session doesn't
keep a stale epic.

## Tests
- Extend `internal/events` tests: claim a child of an epic → row has
  `active_epic_id` = epic; claim a standalone task → NULL; claim the epic
  directly → its own id; release → both NULL.
- Confirm the status-line prefix now renders for an interactive session (an
  existing `tmux_lookup`/`status_line` test seeded with `active_epic_id`).

## Verification
- `go test ./internal/events/... ./internal/monitor/...` green.
- Manually: `endless task claim <epic-child>` then check the session row's
  `active_epic_id`; `endless agents` (E-1621) interactive auto-resolve now works.

## Relations
- Cleans up E-1571 (implements its deferred coordinator-write design).
- Relates to E-1621 (which is blocked-by this task).
