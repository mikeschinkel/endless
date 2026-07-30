Touch-points: the task-type registry (SAME place E-1658's category 'accepts'
map and the parallel brainstorm-type work edit — sequence these, don't
parallelize blindly); `internal/tasktype` Parse/String/Label + the task_types
seed rows; CLI --type values + help + validation messages; default/pre-authorized
type handling (default becomes 'todo'; 'bugfix' pre-authorized for fixes); docs
(endless guide, CLAUDE.md, memory).

DEPENDENCY (revised 2026-07-25 — WAS: blocked on the E-1671 upcasting epic).
The rename does NOT require the upcasting pipeline. Type is stored in the DB as
a stable integer `type_id` (TaskTypeTask=1); the slug is only a label on that
integer. Historical `task.created` / `task.fields_updated` events carry the
slug STRING and resolve at replay through a single chokepoint —
`tasktype.Parse()` (via `projectorTypeID`). So the literal need is: `Parse()`
accepts the legacy slugs 'task'/'bug' as aliases for TaskTypeTask/TaskTypeBug
while `String()` emits the new 'todo'/'bugfix'. That ~4-line alias makes every
historical event replay correctly with ZERO pipeline; no historical DB row
changes (type_id stays 1). The former E-1671 block is downgraded to relates_to:
E-1659 can still serve as the pipeline's proving case retroactively, and the
aliases can later fold INTO the pipeline as a seed migration (E-1671 workstream
7) so `Parse()` doesn't accumulate legacy cruft indefinitely. This is the E-1708
sliver-over-epic pattern in the concrete: a whole epic was gating a rename that
needs a one-function alias.

IMPLEMENTATION now includes: add legacy-slug aliases to `tasktype.Parse()`.

Additional action types (e.g. 'feature') may come later; not filing a
maybe-task for that now.
