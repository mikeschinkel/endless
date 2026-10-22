Only one session can claim a task at a time: `sessions.task_id` is the ownership record (ED-1560). `task claim` enforces this through `_check_task_ownership`, and `task spawn` and `task prime` through `_check_task_ownership` and `_check_prior_claim`.

Two other paths set `sessions.task_id` without either check:

- `task bind` (`bind_item` in `src/endless/task_cmd.py`) refuses only when the target session already holds a different task. It never checks whether another session already holds this one.
- The SessionStart cwd auto-bind (`autoBindFromCwd` in `internal/hookcmd/claude.go`) is guarded by the worktree lock, not by ownership. It does not bind when a live process other than this session holds the worktree's `.endless/worktree.lock`. But only SessionStart in a worktree takes that lock: a session that ran `task claim` from the main checkout and then moved into the worktree holds the task without holding the lock. A second Claude session started in that worktree then takes the lock and binds to the task while the first still holds it.

Today the only thing that notices is the board: `session_stewardship.go` raises the duplicate mark when two present sessions hold one task. The main database had no such case on 2026-10-22, so this has not bitten yet.

Found while planning E-2414, when a "most recently active" tie-break for two sessions holding one task turned out to rest on this gap.
