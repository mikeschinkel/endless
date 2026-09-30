Today .endless/events/*.jsonl (and after E-1197, .endless/db-ledger/db-entries-*.jsonl) only get committed when 'endless worktree land' runs — they accumulate uncommitted on main between landings.

Result: any session that runs endless commands fills the working tree with uncommitted database write-ahead state, which then needs cleanup before any subsequent land.

Sooner-than-later priority because the current friction blocks landing.
