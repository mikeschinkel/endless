Today the UserPromptSubmit handler in cmd/endless-hook/claude.go calls writeClaudeCompanion only when CompanionExists returns false (the E-1011 backfill semantic).

That leaves the companion file vulnerable to drift any time active_task_id changes through a path that doesn't trigger writeClaudeCompanion.
