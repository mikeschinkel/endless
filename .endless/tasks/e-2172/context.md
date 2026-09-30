E-1881 rebases worktrees down from main, but its hard constraint excludes every worktree with a live session or a process holding cwd inside — a rebase rewrites history under whoever is sitting there, and it says outright that this is not a tunable.

So the job reaches every worktree except the ones drifting fastest, because those are the ones in use.

Only the agent inside can rebase a live worktree, and today nothing tells it how far behind it is, gives it a verb to do it safely, or picks a moment when the tree is clean.

Measured: E-1969's session ran ~200 commits behind for its whole life and silently read a different program than the one it was debugging.
