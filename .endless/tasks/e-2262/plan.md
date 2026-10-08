# Have a successful land set the task to assumed

## Status

- Add **`unlanded`** to the transition table (internal/taskstatus/transitions.go;
  regenerate with `just lifecycle-index`): `unverified → unlanded` on a passing
  verify; `unlanded → assumed` on land; `unlanded → unverified` when the branch
  moves past the passed commit.
- **The pass records its commit.** Any `endless` read of the task compares it
  with the branch's latest commit and records `unlanded → unverified` when they
  differ. No git hook.

## Who sets `unlanded` — OPEN, answer before approving

E-2263's user-context runner does not exist yet, so something else must set
`unlanded` or nothing can land in between.

| Option | Pros | Cons |
|---|---|---|
| A. `endless task verify` sets it on a pass only when the run is not an agent's (agentenv's distinction) | Land works from day one; an agent's working runs never count | Rests on agent detection, which E-2266 may find buggy |
| B. Any passing run sets it | Simplest | An agent's working runs would unlock land without Mike's verify |
| C. Move `unlanded` and the land restriction into E-2263 | This task stays small | Splits land's new behaviour across two tasks |

Recommendation: A.

## Land

- Refuses unless the task is `unlanded` and its passed commit is the branch's
  latest commit.
- On success, sets `assumed` when the task's type settles on land.
- **Settles-on-land is a column on `task_types`**, like `auto_spawnable`: true
  for `todo` and `bugfix`, and for future types such as `docs`.
- **Refuses `research` and `brainstorm` outright**: their deliverable is the
  outcome text, never files. Their worktrees stay for now.
- **`land --keep-status`** opts out per run. No per-task field here.
- Implemented where land lives today, in Python.

## Verify

A suite proving: a user pass sets `unlanded`, an agent pass does not (per the
open question's answer); a commit after the pass returns the task to
`unverified`; land refuses a non-`unlanded` task, a stale passed commit, and a
research or brainstorm task; land sets `assumed` for a settling type and keeps
the status with `--keep-status`; the lifecycle diagram is regenerated.
