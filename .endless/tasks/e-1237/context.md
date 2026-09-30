FindWorktreeRoot at internal/monitor/worktree_lock.go walks up from cwd looking for .endless/worktree.json.

Callers (handleWorktreeAdoption, shouldSkipForWorktreeAt) do not validate the returned path is inside projectRoot, so foreign companions could trigger adoption of a worktree outside the registered project.

Surfaced as out-of-scope during E-1219 audit.
