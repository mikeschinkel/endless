Step 4's rebase raises directly rather than retrying, and _is_retryable_ff_merge_error neither matches lock contention nor is consulted there.

Endless itself is the usual holder (the session monitor, the per-minute worktree-unlanded job, worktree check/sync), so collision odds scale with worktree count — one per active task by design — and the message reads like repository damage rather than a transient lock.
