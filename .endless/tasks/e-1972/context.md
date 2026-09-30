A worktree's `.claude/settings.json` pins every Claude hook to that worktree's own `bin/endless-go` (E-998, so spawned sessions test candidate hook code).

When that binary is stale it aborts before doing anything — a Jul 29 build dies on the E-1659 `task`→`todo` integrity check — so SessionStart never registers the session and the Claude window runs completely untracked, with the error on hook stderr nobody reads.

Measured today: 135 worktrees, 89 pinned to their own binary, and all 103 existing worktree binaries older than main's.
