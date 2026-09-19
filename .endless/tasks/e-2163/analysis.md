`task remove --cascade` walks the LITERAL parent_id through `live_tasks`, so it stops at the first removed task in the chain — but since E-2161 the tree a user sees walks `effective_parent_id` and steps OVER that removed task. The two no longer describe the same subtree, and the removal is the one that is destructive.

Reproduce: task 1 (live) → task 2 (removed) → task 3 (live).

- `task_tree` renders 3 under 1 — `task show E-1 --children` lists it, `task list --parent E-1` returns it.
- `task remove E-1 --cascade` covers `{1}`. Task 3 survives and re-renders at the root.
- `task remove E-1` with NO `--cascade` is not even refused: the guard counts `live_tasks WHERE parent_id = 1`, finds zero (2 is removed, and 3 points at 2), and removes 1 silently.

So a user can remove a subtree and leave behind a task that was visibly inside it, and can take the non-cascade path precisely where the guard exists to stop them.

Before E-2161 the same child was re-rooted to NULL on removal, so it also escaped the cascade — but it did not LOOK enclosed either. What is new is the disagreement between what the tree shows and what the removal reaches, not the escape itself.

Not folded into E-2161: that plan settled "cascade removal is unaffected", and closing this widens the reach of a destructive operation, which is a decision rather than a bug fix. It also has to move `removeTaskTree` (internal/events/task_removal.go) and `_removal_id_set` (src/endless/task_cmd.py) in lockstep — the executor and the projector share the Go one, so a Python-only change would make a rebuild disagree with the live path about what a removal covered.

That lockstep is why this is blocked by E-1063: once the CLI is Go, the enumeration is one implementation to change instead of two that must not drift.

Open when it is picked up: whether the cascade adopts `effective_parent_id` (removal follows the render), or the render keeps `effective_parent_id` while the non-cascade GUARD learns to count effective children (removal stays literal, but refuses to strand anything visible). The second is the smaller change and keeps `--cascade` meaning "the subtree you filed"; the first makes the two agree outright. Both are defensible; E-2161's own split — parent_id is what the user set, effective_parent_id is where it renders — argues for the second, since a cascade is a mutation and mutations kept naming parent_id.
