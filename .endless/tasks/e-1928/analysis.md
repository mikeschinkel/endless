`remove_item` in `src/endless/task_cmd.py` emits `task.deleted` and THEN counts
the descendants it is about to report:

    emit_event(kind="task.deleted", ...)     # the Go executor deletes the subtree here
    ...
    desc_count = db.scalar(
        "WITH RECURSIVE tree(id) AS ("
        "  SELECT id FROM tasks WHERE parent_id = ?"
        "  UNION ALL"
        "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
        ") SELECT count(*) FROM tree", (item_id,))

`emit_event` runs the executor synchronously, so by the time the query runs the
rows are already gone and the recursive CTE seeds from nothing. The count is
therefore always 0.

Reproduced in an isolated DB — a parent with a child and a grandchild:

    $ endless task remove 8600 --cascade
    • Removed E-8600 and 0 descendant(s): P

Two tasks were deleted alongside the root; the line says none were.

Pre-existing, not introduced by E-1915 — E-1915's guard raises before
`emit_event` and does not touch this path. Found while verifying the
`--cascade` behavior.

The fix is to capture `desc_count` BEFORE the emit, next to the existing
`child_count` read. E-1915 already computes exactly this id set before the emit
(`_removal_id_set(item_id, cascade)`), so the descendant count is
`len(_removal_id_set(item_id, True)) - 1` and no second query is needed.
