endless worktree land auto-commits .endless/events/*.jsonl as part of landing, but a direct git merge does not.

Events accumulate untracked, breaking clone-completeness.
