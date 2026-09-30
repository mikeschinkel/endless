(a) UNCLAIMED: session 439 edited internal/monitor/migrate.go and baseline_test.go with no claim at all and the hook allowed it — either PreToolUse on Edit and Write is not checking session-to-task binding, or an enforcement flag defaults off that should default on for endless itself.

(b) CLAIMED BUT ELSEWHERE, added 2026-08-16: a session whose claimed task has a worktree, but whose cwd is outside it, edits the main checkout unguarded.
