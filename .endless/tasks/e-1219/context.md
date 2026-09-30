FindWorktreeRoot at internal/monitor/worktree_lock.go walks up from cwd looking for .endless/worktree.json and stops at projectRoot.

Pre-E-1218, main's tracked stale companion caused the walk-up to claim a lock on main itself (observed: my E-1209 session at SessionStart wrote .endless/worktree.lock at the project root with my session's PID).

Surfaced by sibling session during E-1218 cleanup; not bundled because separate fix site and out of E-1209's land/SessionEnd scope.
