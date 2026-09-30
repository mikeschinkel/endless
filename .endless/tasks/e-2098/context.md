`endless worktree sync` reports and closes worktree drift, but only when a person runs it.

Nothing surfaces that a worktree is behind, so a change to a file every task shares reaches main and reaches no working checkout until someone thinks to sweep — which is how 138 of 142 worktrees came to predate a guard they all depend on.

There is also no way to watch a sweep's progress or be told when the fleet is current.
