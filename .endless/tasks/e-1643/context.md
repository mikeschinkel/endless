Today a task can spawn additional worktrees at .endless/worktrees/e-<id>-<slug>/.

Mike wants to guard toward one-worktree-per-task and instead model legitimate multi-checkout needs (A/B implementations, run-while-edit, bisect/snapshot-compare) as CHILD TASKS of the core task, each with its own plan, plus a compare/pick task.
