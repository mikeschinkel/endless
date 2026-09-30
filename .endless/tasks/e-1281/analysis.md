Worktrees are conceptually isolated; the DB writes should be too.

Investigation needed: DB-path resolution, isolation approaches (per-worktree DB, env override, sandbox flag), migration of in-flight state.
