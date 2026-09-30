Common triggers: (a) ENDLESS_PROJECT_ROOT/ENDLESS_WORKTREE_PATH env vars from a stale esu activation pointing at a worktree's empty .endless/, (b) cwd-walk landing in a worktree whose .endless/ has no DB (see E-1158).

esp itself crashes on this path too — fixing the diagnostic auto-fixes esp's UX. Discovered 2026-05-03 verifying E-1114.
