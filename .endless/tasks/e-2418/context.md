Only one session can claim a task at a time: `sessions.task_id` is the ownership record (ED-1560). `task claim` enforces this through `_check_task_ownership`, and `task spawn` and `task prime` through `_check_task_ownership` and `_check_prior_claim`.

Two other paths set `sessions.task_id` without either check:

- `task bind` (`bind_item` in `src/endless/task_cmd.py`) refuses only when the target session already holds a different task. It never checks whether another session already holds this one.
- The SessionStart cwd auto-bind (`BindSessionToTask` in `internal/monitor/session.go`) sets `task_id` unconditionally. Its dedup ends other rows for the task only when they have no pane, so a second Claude session started in a live task's worktree binds to that task while the first still holds it.

Today the only thing that notices is the board: `session_stewardship.go` raises the duplicate mark when two present sessions hold one task. The main database had no such case on 2026-10-22, so this has not bitten yet.

Found while planning E-2414, when a "most recently active" tie-break for two sessions holding one task turned out to rest on this gap.
