Three directories exist under .endless/worktrees/ but git doesn't recognize them as worktrees — their .git files point to stale metadata at .git/worktrees/task-1031-session-focus etc. (paths that no longer exist).

They're invisible to endless worktree list.

Likely leftovers from pre-complete worktree functionality.

Currently their .endless/db-ledger/ dirs contain main's current ledger split (copied during the E-1310 hygiene sync; harmless filler) but the rest of each directory's working tree still reflects its pre-orphan state.
