session respawn <id> resolves session -> (task, that session's worktree) and runs the reopen core anchored via --worktree; session respawn --task E-NNN resolves to the most-applicable session for the task (same live-first / skip-ghost rule).

Both refuse when a session is already live for the task (navigate to it).

Foreground + --bg parity with spawn.
