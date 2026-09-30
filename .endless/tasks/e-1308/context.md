It isn't; the branch is in main.

The worktree is NOT removed by landing: land leaves the directory and branch in place, and the reaper later removes BOTH (`git worktree remove` + `git branch -D`) once the task's task_landings row is older than worktree_ttl (default 14 days), or a human removed it manually (`git worktree remove` / `endless worktree drop`).

So a gone worktree is a later condition, not an immediate-after-land one — an immediate multi-session re-land still finds the worktree present and re-lands idempotently.
