The DB ledger files (.endless/db-ledger/db-entries-*.jsonl) are append-only event logs but lack a merge driver in .gitattributes.

Concurrent appends on main and a worktree branch produce a 'CONFLICT (content)' on rebase even when both sides only added non-overlapping lines (the worktree branch's lines often turn out to be a subset of main's, since the same events landed in both via parallel auto-record commits).

Will eliminate the recurring 'rebase failed on ledger' that has bitten multiple sessions and currently requires the manual 'git checkout main -- .endless/db-ledger/*.jsonl + git rebase --skip' dance.
