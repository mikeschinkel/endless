Three sources of truth currently encode 'what task does this worktree belong to': the DB sessions.active_task_id (canonical, what tmux trusts), the path convention .endless/worktrees/e-NNN/ (cheap derivation), and the .endless/worktree.json companion's task_id field (drifts when leftover).

The companion's task_id can lie: a stale file in main reported E-1186 while tmux correctly showed E-1298, defeating E-1298's first fix attempt.
