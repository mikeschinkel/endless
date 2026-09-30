For epic-shaped worktrees whose commits are purely .endless/ metadata files (plans, analyses, ledger entries), the pre-land Go build in 'just land' (which calls 'just go') is dead weight — it doesn't verify anything that changed.

Surfaced 2026-06-29 when 'just land E-1537' (epic-shaped) failed the rebuild step because 'just go-work-init' hadn't been re-run in the worktree; the build was unnecessary since the commits were all .endless/ metadata.
