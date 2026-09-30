Blocking is task-scoped, but the real dependency is often a sub-scope sliver.

Example: E-101 needs only a trivial piece of E-100 (e.g. one new enum value), yet E-100 is itself blocked by a large epic unrelated to E-101 — so E-101 is transitively dragged behind that whole epic when it only needed a one-liner.

This delays landings and lets main drift, breeding rebase/integrity conflicts in the waiting worktree.
