After land, the post-land reaper iterates on-disk worktree dirs and calls 'git worktree remove --force <dir>'.

e-1396 is the current trip-wire: empty dir on disk since 2026-06-11, reproduces on each 'just land'.
