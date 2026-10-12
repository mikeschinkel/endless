# Plan: make task bind the planning step before claim

`session_tasks` should record how a session came to own its task. Today it learns that only through the `task.claimed` executor, and two of the three ways a session comes to own a task never reach it:
- `task bind` emits `task.claimed`, so it records `claimed` even though nothing was claimed.
- The SessionStart cwd auto-bind (`maybeCwdBind` → `monitor.BindSessionToTask`) writes `sessions.task_id` directly, with no event and no `session_tasks` row. Spawn's claim skips `task.claimed` because no session exists yet to name, so a spawned session's own task has no ownership row at all. For example, ES-1314's row for E-2283 reads `revisited`.

Separately, claim's no-plan refusal never mentions `task bind`. So a session drafting a plan with its user concludes the gate has to be loosened.

Ownership itself stays in `sessions.task_id` (`_task_claimants`, the write gate). Nothing here changes who owns a task. It changes only how that ownership is labelled and listed.

## 1. A `bound` relation (ED-1497 relations; rank as defined in ED-1622)
- Append `RelationBound = 6` to `internal/sessiontaskrelation`, with slug `bound` and label `Bound`. Never renumber. Add the `schema.sql` seed row and a migration. `VerifyIntegrity` fails closed on drift.
- Rank: directly below `claimed`, above `queued`. A later `task claim` by the same session then upgrades `bound` to `claimed`, and binding a queued task upgrades `queued` to `bound`.
- In `session status`'s display tier (`prominence` in `relation_tier.go`), `bound` counts as decided work, alongside `claimed` and `queued`.

## 2. Which relation each path writes
- `task claim` → `claimed`. No change.
- `task bind` → `bound`, whatever the task's status (Q1 a).
- The SessionStart cwd auto-bind → decided by the task's status at bind time (auto-bind a):
  - `underway` (spawn claimed it for this session) → `claimed`
  - pre-work (`unplanned`/`submitted`/`ready`, i.e. a primed session, ED-1621) → `bound`
  - any other status → `bound`
  
  It writes the row in the same transaction as the `sessions` upsert in `BindSessionToTask`.

## 3. Bind gets its own event (Q3 a)
- A new kind, `task.bound`, emitted by `bind_item`. Its executor shares the `sessions` UPDATE (task_id, revive an ended row, epic_id) with the `task.claimed` executor through one helper, so the two are not copies. It upserts `RelationBound`.
- Old bind events in the ledger stay `task.claimed`, so they rebuild as `claimed`. They cannot be told apart after the fact, and that is accepted.
- Check other readers of `KindTaskClaimed` (for example the `claim-event` reason in `usermachinelog.go`) and give `task.bound` its own reason where they log one.

## 4. Readers that special-case `claimed`
- **`session status`:** `nonClaimedRelationIDs` becomes "every relation except the anchor relations". The anchor relations are a named set, `{claimed, bound}`, defined in `sessiontaskrelation`, so a bound task is the session's anchor and not a second row (Q4 a).
- **`session task remove`:** refuses a `bound` task exactly as it refuses a `claimed` one, and points to `session hide --task` (Q6 a). `sessions.task_id` stays set either way, so allowing it would leave the list out of step with ownership.
- **`session task add`:** queuing your own bound task leaves it `bound`, reported rather than raised as an error, as it already does for `claimed`.

## 5. Claim's no-plan refusal points to bind (Q8 a)
- In `_require_spawnable`, when the verb is `claim` and there is no plan, add a conditional route: "If you and your user are working out the plan together now: `endless task bind <id>`. Claim once the plan is attached." Do not add it for `spawn` or `prime`.
- Update the `task bind` section of `endless guide tasks` with the same wording, and update `task bind`'s help and docstring. Bind's documented use is no longer only already-shipped tasks.

## 6. Writes before a plan stay refused (Q7 a)
- No new rule. A bound session passes `sessionMayWrite`, but bind creates no worktree, and the worktree gate (`enforceWorktreeGate`) refuses edits in main.
- Add a hook test that pins this: a session bound to an unplanned task, with no worktree, has its Write in main refused.

## Tests
- Go:
  - the relation enum, rank and seed integrity
  - the `task.bound` executor writes `sessions.task_id` and `bound`
  - a claim after a bind upgrades the row to `claimed`
  - binding a queued task upgrades it to `bound`
  - the cwd auto-bind writes `claimed` for an `underway` task and `bound` for a pre-work one
  - `session status` shows a bound task only as the anchor
  - `session task remove` refuses a bound task
  - the hook test in section 6
- Python:
  - `task bind` emits `task.bound`
  - claim's no-plan refusal includes the bind line, and spawn's does not

## Out of scope
- Backfilling `session_tasks` rows for sessions bound before this lands.
