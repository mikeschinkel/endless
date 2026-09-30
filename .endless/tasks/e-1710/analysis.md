Method: enumerate real-DB rows whose creation is event-sourced (tasks, decisions, etc.) and confirm each has its originating event (e.g. task.created) in main's committed .endless/db-ledger; report any row whose event is absent from main's committed ledger (only on an unlanded/dropped worktree branch, or missing).

Also characterize the observed duplication: E-1709's task.created is committed BOTH on main (7e207d8d) AND as a redundant worktree-branch commit (3a3aa8b4) — determine whether --db main run from a worktree double-records, and whether that is benign.

Deliverable in outcome: findings, whether any real loss occurred, and any fix filed as a SEPARATE follow-up.

Findings-only, not a fix.
