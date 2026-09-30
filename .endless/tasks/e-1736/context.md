A commit on a task branch touching .endless/db-ledger/ is always wrong: ledger auto-commits belong to the main checkout only (E-1309), and a branch-side ledger commit rides the land rebase into main.
