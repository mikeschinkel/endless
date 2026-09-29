Fix: in land_worktree, wrap the post-ff-merge task.landed emit so a failure surfaces "Landed <id> (main advanced) but recording the landing failed: <cause>; re-run just land <id> to record it (the ff-merge is idempotent)" instead of re-raising as total failure.

Narrow hardening; the trigger we hit (attribution) is removed by E-1470, but other emit failures would leave the same partial state.
