E-1993's plan gate (`_require_spawnable` in `src/endless/task_cmd.py`) refuses to claim an unplanned task, because a claim starts work and work runs from a plan. A session still working out a task's plan with Mike needs to own the task before then, for its session_tasks associations.

`task bind` already gives it that ownership. It sets `sessions.task_id` without changing status, creating a worktree or passing the gate. A later claim from the same session goes through the gate and moves the task to underway, the same path primed sessions take. Bind falls short in two ways, and both come from treating it as a claim rather than as the step before one:

1. **Nobody finds it.** The no-plan refusal offers only writing a plan or `task prime`, never bind. The gomion session (from E-2283) therefore concluded the gate had to be loosened. The bind line belongs in the claim refusal only. Spawn hands the task to a new session, which has nobody to plan with.

2. **It records a claim.** `bind_item` emits the same `task.claimed` event that `_perform_claim_work` does. The executor upserts `RelationClaimed` into session_tasks for both. A bound, still-unplanned task therefore shows "Claimed: ES-…", and neither the ledger nor session_tasks can tell bind from claim. That is the mislabel E-1967 renamed `goal` to `claimed` to remove.

The fix for (2):
- Append `RelationBound = 6` in `internal/sessiontaskrelation`. Never renumber. It needs the schema.sql seed row, a migration and a Rank below claimed; VerifyIntegrity fails closed on drift.
- Make bind distinguishable in the event, either as a new `task.bound` kind or as a payload flag. Old bind events stay `claimed` on rebuild.
- A later claim by the same session then promotes the row to `claimed` through the strongest-relation upsert.
- Decide how `bound` behaves in each reader that special-cases `claimed`: the claimed-task exclusion in `session_status.go`, the claimed/queued grouping in `relation_tier.go`, and the claimed branch in `session_task_membership.go`.

Folded together under ED-1550: these are one cause, and this task was open.
