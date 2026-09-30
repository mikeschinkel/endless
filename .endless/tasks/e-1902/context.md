A worktree dir is the only home for uncommitted and untracked work, which blocks aggressive reaping and Time Machine exclusion of the 84,303 derived files (47%) under .endless/worktrees.

— verified to survive 'worktree remove --force', 'branch -D' and 'gc --prune=now --aggressive'.

Full verified evidence in analysis.
