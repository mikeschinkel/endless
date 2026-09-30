Measured 2026-09-23: 118 worktrees, 94 still pinning every Claude hook at their own bin/endless-go via .claude/settings.local.json, roughly a third of those binaries stale.

A stale one aborts before registering the session, auto-registers stray project rows, and has twice resurrected dropped schema objects that broke session writes machine-wide.
