Extend it (or add an immediately-following step) to git-add and commit the just-written snapshot pair.

Commit subject suggestion: 'Endless: capture plan snapshot <stem>' or similar.

Idempotency note: snapshotPlanFile already early-returns when the same content+session hash exists, so commits also won't duplicate.

Goal: after E-1206, E-1208, and this task land, nothing endless-managed remains dirty on main between writes — worktree land's auto-commit step has nothing to do.
