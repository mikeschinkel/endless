E-1012 shipped a PreToolUse gate (blockCommitOnMainIfApplicable, internal/hookcmd/claude.go) that denies `git commit` from main's working tree in a Claude session, and it is confirmed.

A session still committed on main to clear a `worktree land` refusal.
