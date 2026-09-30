Today, worktree .claude/settings.json must include SessionStart and UserPromptSubmit hook entries that invoke endless-hook.

The only mechanism is 'just claude-settings-init' (dev-only).

Surfaced 2026-05-16: worktree settings.json had only enabledPlugins+env, no hooks; SessionStart never fired; no companion; all session resolution broke.
