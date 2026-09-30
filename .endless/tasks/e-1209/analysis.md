Better: have land explicitly remove both files before `git worktree remove`, or pass `--force` when only those files remain.

Either way the user shouldn't see a spurious error every land.
