E-1915 guarded `task_deps` and `decision_relations`, the two tables whose rows
are *relations* and can therefore be cleared with an `unlink`. Four more tables
key on a task id with no foreign key, deliberately, and every one of them
resurrects on id reuse the same way `task_deps` did:

| Table                  | Rows survive a task delete because…                          | What a reused id inherits |
|------------------------|--------------------------------------------------------------|---------------------------|
| `session_tasks`        | "rows must outlive their referenced session/task" (schema)    | A session is reported as having touched a task it never saw. `task show` renders this under "Touched by:". |
| `session_hidden_tasks` | "the row must be able to outlive its session or task"         | A brand-new task is silently invisible in one session's `session status`. Nothing indicates why. |
| `session_notices`      | "a notice must be able to outlive its session or task"        | An undelivered notice about a dead task is delivered as though it were about the new one. |
| `project_next_tasks` / `project_next_pending` | No FK to `tasks` at all (`task_id` is TEXT) | A curated "next" entry for a removed task reappears pointing at unrelated work. |

Measured, not inferred: after `endless task remove 8001` in an isolated DB,
`session_tasks` held 2 rows for 8001 (the executor adds a `revisited` touch on
the delete itself), `session_hidden_tasks` 1, `session_notices` 1,
`project_next_tasks` 1. For contrast, `task_landings` (FK ON DELETE CASCADE),
`sessions.active_task_id` and `session_statuses.active_task_id` (FK ON DELETE
SET NULL) all cleared correctly, and `session_task_relations` turned out to be
the relation-kind vocabulary table (goal/surfaced/revisited), not task-keyed at
all — so the two tables E-1915's plan named as suspects are both clean.

## Why this is not just "apply the E-1915 guard here"

E-1915's answer was *refuse the removal and make the operator unlink*. That
answer does not transfer. There is no `unlink` for "session 9001 touched task
8001" — it is history, not a relation, and the schema comments say outliving the
task is the POINT. So the guard would be unclearable: a task touched by any
session could never be removed.

The real question this task has to answer is one design call, shared by all
four: when a task id is freed, does a row that deliberately outlives its task
stay attached to that id?

Sketch of the options, not a decision:

- **Purge on delete.** Contradicts the stated intent of every one of the four
  schema comments; loses the "session N touched task M" audit record.
- **Tombstone the id.** Stop reusing task ids, or record freed ids, so nothing
  can inherit. Fixes the whole class at once, including anything added later,
  but is the largest change.
- **Qualify the rows.** Store enough alongside the task id (the task's created_at,
  or a generation counter) that a consumer can tell "this row is about the task
  that HELD id 8001", and filter on read.
- **Repair, don't prevent.** Extend E-1915's `reconcile` repair to these tables.
  Cheapest, and it inherits E-1915's ordering hazard: it can only clean orphans
  whose id is still free.

They may not all want the same answer — `session_tasks` is an audit record,
`session_hidden_tasks` is live display state, `project_next_tasks` is a curated
list. Filed as one task because the design call is shared; split if the answers
diverge.

Nothing here is implemented. E-1915's reconcile repair covers `task_deps` and
`decision_relations` only.
