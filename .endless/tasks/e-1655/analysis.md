Drop the e-<id>-* glob and lexicographic-first pick in WorktreePathForTask (internal/monitor/worktree.go); drop the optional -slug from the path regexes (_WORKTREE_TASK_ID_RE in src/endless/worktree_cmd.py, TaskIDFromWorktreePath in internal/monitor/worktree_lock.go); collapse sandbox naming in src/endless/config.py; tidy now-moot slug comments (reap_worktrees.go, db.go); update CLAUDE.md and 'endless guide orchestration' to document the child-task model for multi-checkout needs; update any slug-tolerance tests.

No active block on raw 'git worktree add' -- a non-canonical dir simply stops being recognized as the task's worktree.

No migration.

Anchors by symbol, not line number (they drift).
