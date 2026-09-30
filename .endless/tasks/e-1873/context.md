The handoff templates tell every spawned session "Don't run `endless worktree land`/`drop` without asking", but the guide's 'Landing the work' section says the opposite by omission: 'When the task is verified (or you're using assume): endless worktree land <id>'. The only trace of the rule in the guide is orchestration.md, a sentence *describing what the handoff template carries* — not a rule a session reading the Landing section would see.

A session that re-reads the guide after a compaction will land unasked. Found by the E-1870 guide audit.
