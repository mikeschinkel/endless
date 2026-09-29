E-1361 has a complete history in .endless/db-ledger ending in status=completed with an outcome, and no task.deleted event in any of the 57 segments — but it has no row in the SQLite tasks table at all (absent, not removed).

Its worktree survives on disk as e-1361, which is how it was noticed.
