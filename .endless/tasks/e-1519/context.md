— so verifying a Python-side change becomes a debugging detour for the verification harness.

A fourth instance bites even when targeting --db main from inside a worktree: cwd-derived project resolution wins over --db, requiring --project endless explicitly.
