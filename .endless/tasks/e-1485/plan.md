# E-1485 — Surface closeable parent tasks in the per-project next list

## Decided (Mike, 2026-05-27)

The curated next list (E-1421) is session-authored, so closeable parents must be
**written into the list as pending items**, NOT computed at render time — a
curating session has to see them to pull them into a ranked lane.

- **Target:** the persistent **`project_next_pending`** table (schema.sql:330) —
  separate from `project_next_lanes`/`project_next_items`, so it **survives a
  `revise`** (which only rewrites lanes/items). This is the "uncategorized /
  pending" bucket. Dedup via `UNIQUE(project_next_id, task_id)` → `INSERT OR IGNORE`.
- **"Closeable parent"** = a task whose children are ALL terminal
  (confirmed / assumed / declined / obsolete) while the parent itself is NON-terminal.

## Ground truth / scope

`project_next_pending` exists in the schema but has **no production writer** today
(only a test inserts). The E-1421-design "auto-add urgent" was never built. So
E-1485 is the **first writer** of this table and defines the add-to-pending path
that a later urgent-auto-add can reuse.

## Mechanism

Maintain `project_next_pending` as a derived projection of task state:
- When a task changes to a terminal status, re-evaluate its parent: if all the
  parent's children are terminal and the parent is non-terminal →
  `INSERT OR IGNORE` the parent into `project_next_pending`
  (reason e.g. "all children terminal", `added_at = now`).
- Remove the parent from pending when it's no longer closeable (a new non-terminal
  child appears / a child reopens) or when the parent reaches a terminal status.
- Render: `endless task next` shows pending items after the ranked lanes. If a task
  is already in a curated lane, don't also show it in pending. (Verify whether
  `task next` already reads `project_next_pending`; wire it if not.)

## Trigger (decided 2026-05-27 — by E-1437 precedent)

**Event subscriber in the task-mutation code path**, NOT a prompt-hook scan.
E-1437 already establishes this pattern (subscribes to `task.created` /
`task.phase_changed` to write urgent tasks into `project_next_pending`). E-1485
reuses it: subscribe to a task's status change to a **terminal** status; on each,
re-evaluate the changed task's parent — if all the parent's children are now
terminal and the parent is in an open status, `INSERT OR IGNORE` the parent into
`project_next_pending` (reason "all children terminal"). Mirror E-1437's
single-row `INSERT ... ON CONFLICT DO NOTHING` (no BEGIN IMMEDIATE needed). Blocked
by E-1437 so the subscriber infrastructure is built once, not duplicated.

## Clearing (decided)

The same subscriber removes the parent from `project_next_pending` once it no
longer "needs attention to close": when the parent's status changes to `verify`
or any terminal status (the user has acted on it), OR when it becomes non-closeable
again (a new non-terminal child appears / a child reopens). So a closeable parent
appears in pending only while it is still open with all children terminal.

## Relations

relates_to E-1421 (the curated next list this extends).
