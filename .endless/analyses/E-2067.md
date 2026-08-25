## Where the hole is, precisely

- `internal/events/executor.go`, execTaskMoved: after the ValidateMaybeParentless
  call it loops up the parent chain from the target parent and returns
  "circular reference: task %s is an ancestor of target parent %d". This is the
  guard that works.
- `internal/events/executor.go`, execTaskFieldsUpdated: its allowedFields map
  includes `"parent_id": "parent_id"`, and the handler runs the maybe-phase
  check on a parent change but never the ancestor walk. This is the hole.

So the validation is not missing — it is in one of the two executors that write
the same column. That also tells you the shape of the fix: the maybe-phase rule
was already factored out into a shared `ValidateMaybeParentless` and is called
from both. Do the same with the ancestor walk.

## Repro

    endless task update E-A --parent B      # while B.parent_id is still A

Both rows now point at each other. `endless task show --children`, session
status and the epic-status derivation all walk the tree recursively.

## Explicitly NOT in scope: clearing a parent

There is already a documented way, and it needs no work. `endless task update`
prints:

    --parent TASK_ID   Set parent task ID (0 to make root)

and `endless task move <id> --root` does the same thing. The session that filed
this invented a non-existent `--clear-parent` rather than reading the help, then
set one side of the relationship without clearing the other. Only the missing
cycle guard is a real defect; the clearing mechanism is present and discoverable.

## Related but separate

E-2018 ("Enforce the documented status lifecycle on task update") is the same
SHAPE of defect on a different column — `task update` bypassing a rule another
path enforces. Worth a glance for a common factoring, not a merge.
