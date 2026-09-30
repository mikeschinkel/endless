`just land` runs `endless db apply-change` and `endless worktree land`'s record step using the worktree's bin/endless-go, but only runs `just build` AFTER those steps (the trailing 'Refreshing binaries').

It assumes the worktree binary is already current, and the E-1664 guard only checks it's PRESENT, not CURRENT.
