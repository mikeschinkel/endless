Worktree-land's main-side Step 3 currently auto-commits any file matching AUTO_COMMIT_GLOBS into 'Endless: auto-record session activity', regardless of whether it is a legitimate writer artifact or stray junk (e.g., an empty 'touch'd test file).

Surfaced during E-1416 manual verification when an empty .endless/db-ledger/test-e1416-verify.jsonl got bundled silently into commit 0b6cea1, then had to be dropped by 8b78055.

The new worktree-side guard (Step 3.8 from E-1416) refuses loudly on the same dirt class;
