# Have a successful land set the task to assumed

## Status

- Add **`unlanded`** to the transition table (internal/taskstatus/transitions.go;
  regenerate with `just lifecycle-index`): `unverified → unlanded` on a passing
  verify; `unlanded → assumed` on land; `unlanded → unverified` when the branch
  moves past the passed commit. Plus the user's reopen/decline/obsolete edges,
  as `unverified` has.
- **The pass records its commit**: the full SHA rides on the
  `task.status_changed` event into `unlanded` and is projected into
  `tasks.verified_sha` (migration 00018), so a ledger rebuild reproduces it.

## Who sets `unlanded` — DECIDED: A

`endless task verify` sets it on a pass only when the run is not an agent's
(agentenv's `Present`). An agent's own runs are still recorded as CTRF reports
but never unlock land. The `task update --status` actor guard also refuses an
agent setting `unlanded` by hand.

## Staleness — DECIDED

- A pass is stale when `git diff <passed> task/<id>` shows any change outside
  the paths Endless manages (`.endless/db-ledger/`, `.endless/verbs.jsonl`) —
  a content diff, not a literal SHA match, because Endless commits ledger
  entries onto the branch after a verify.
- Checked by one Go reconcile (`endless-go task reconcile-unlanded`), which only
  looks at tasks currently `unlanded`. Called from `task show`, `task list`,
  `task next`, `task verify` and `worktree land`.

## Land

- **Gate by type.** todo and bugfix require a verify suite: land refuses unless
  the task is `unlanded` with a fresh pass. research and brainstorm are refused
  outright (their deliverable is the outcome text). Any other type (a future
  `docs`) is gated only when the task has a suite.
- **`task_types` gains two columns**, mirrored from `tasktype` and checked by
  `VerifyIntegrity`, like `auto_spawnable`: `requires_verify_suite` and
  `settles_on_land`, true for todo and bugfix.
- On success, sets `assumed` when the type settles on land.
- **`land --keep-status`** opts out of settling per run. No per-task field.
- `--record-only` (backfill of a past land) skips both the gate and settling.
- Implemented where land lives today, in Python.

## Verify

A suite proving: a user pass sets `unlanded`, an agent pass does not; a commit
after the pass returns the task to `unverified`, a ledger-only commit does not;
land refuses a non-`unlanded` todo, a stale pass, and a research or brainstorm
task; land sets `assumed` for a settling type and keeps the status with
`--keep-status`; the lifecycle diagram is regenerated.
