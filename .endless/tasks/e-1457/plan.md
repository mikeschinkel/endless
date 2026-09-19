Implement E-1457 in its own worktree/branch (NOT the e-1449 worktree).

The endless repo has two `.gitignore` problems with Claude Code settings files:

1. **Ignore the local settings files.** `.claude/settings.local.json` (Claude
   Code's per-developer local overrides — local permission allow-rules, etc.)
   is untracked and unignored, showing as `git status` dirt. There are two
   instances today: `.claude/settings.local.json` and
   `demo/.claude/settings.local.json`. Add a single bare line
   `settings.local.json` (NO leading slash) to `.gitignore`. A no-slash pattern
   matches the basename at any depth, so this one line covers both instances and
   any future ones. (The bare basename is intentionally broad; `settings.local.json`
   is exclusively Claude's local-settings convention, so there's no collision
   risk in this repo.)

2. **Remove the dead `.claude/settings.json` entry.** The `.gitignore` currently
   ends the E-998 block with `.claude/settings.json` plus a comment admitting it
   is "mostly documentary — gitignore does not affect tracked files." That entry
   is a no-op: `.claude/settings.json` is a tracked file, so the rule has no
   effect, and it's misleading. Remove the entry. Revise the comment block: drop
   the "documentary" sentence, but keep a short note that `.claude/settings.json`
   is tracked and kept per-worktree via `git update-index --skip-worktree` (run
   by `just claude-settings-init`) — so nobody re-adds a useless gitignore line.
   The skip-worktree mechanism is unaffected by this change.

Verify:
- `git status` in the main checkout no longer lists `.claude/settings.local.json`
  or `demo/.claude/settings.local.json` as untracked.
- `git check-ignore -v .claude/settings.local.json` and
  `git check-ignore -v demo/.claude/settings.local.json` both report the new
  bare rule.
- `.claude/settings.json` is still tracked (`git ls-files .claude/`) and
  worktree regeneration still doesn't dirty status (skip-worktree intact).
- No tracked file named `settings.local.json` exists that the bare pattern would
  unexpectedly start ignoring (`git ls-files | grep settings.local.json` → empty).

This is a small, fully-specified change with no design decisions. Commit on its
own branch, then drive to closure: set the task to `verify` with a
"To verify: ..." handoff and ask Mike whether to land or wait. Do not land
autonomously.
