An agent cannot move its own working directory — only the user can, via the harness cd command — so the hook must BLOCK and emit the ready-to-run line naming the worktree path, for the user to run.

Both conditions are the same enforcement path and one fix.

E-1964 deletes that mechanism, so do not key the check on it.
