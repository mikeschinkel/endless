Removing a task keeps its row (removed=1) so ids are never reused, but the parent link is discarded: non-cascade removal and `task import --replace` both run UPDATE tasks SET parent_id = NULL on the removed task's children, mirroring the ON DELETE SET NULL the hard-delete path used.

For a LIVE child that is deliberate — tree renders join live_tasks to live_tasks, so a live child under a hidden parent would vanish from every render — but the same statement also nulls children that are themselves already removed, where it protects no render and simply destroys the association.

There is no restore verb, so nothing in the live database can reconstruct it; only the ledger can, since replay shares this code and the child's earlier events still carry the original parent_id.
