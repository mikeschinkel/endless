endless worktree land Step 5 (ff-merge) only retries on 'uncommitted' / 'would be overwritten' errors — worktree-side concurrent writers.

Observed during E-1347 land 2026-05-15: main got commit 666a038 41 seconds after the rebase, blocking ff-merge.
