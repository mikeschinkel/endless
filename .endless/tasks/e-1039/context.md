/usr/local/bin/endless-go is a single global symlink into the MAIN checkout's bin/.

Two neighbours already solved adjacent halves: E-998 points a worktree Claude session's hooks at the worktree's own bin/endless-go hook claude via its .claude/settings.json, and E-1368 made the binary self-detect the sandbox DB from cwd -- but that routes the DATABASE, not which binary runs.
