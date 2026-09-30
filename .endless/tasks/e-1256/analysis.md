Symmetric counterpart to `endless task release`: bind sets `sessions.active_task_id`, release clears it.

`endless task claim` continues to mean "start working on this task" (changes status to in_progress, creates worktree); `bind` is the lower-impact alternative for display only.

Implementation goes through a new `task.bound` event (mirroring `task.released`) so the Go executor handles the sessions DB write per the no-Python-SQLite-writes-in-new-code rule.
