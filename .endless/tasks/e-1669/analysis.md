at creation, copy the installed endless-go into <worktree>/bin/ (valid immediately since it matches the worktree's main-equal code; 'just build' overwrites it in place later) and repoint the hook in <worktree>/.claude/settings.json at it — porting the claude-settings-init (E-998) logic into code so the claim/spawn verb need not shell out to just.

Scope: hook only; tmux/channel stay on main's build (shared infra, tested deliberately); the bare-shell foreign-build case is the separate E-1668.
