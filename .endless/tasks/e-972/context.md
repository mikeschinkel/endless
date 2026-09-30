Result: bifurcated event logs with the same project's writes split across multiple .endless/events/ directories. Discovered when another session reported strange behavior from inside the e-967 worktree; confirmed by comparing event-log file sizes between main and worktree.

Worktree's events file at the time of the fix had 150651 bytes of strays
