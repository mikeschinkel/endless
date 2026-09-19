Mechanism enforcement for the per-session-worktree discipline. Standalone follow-up to E-971; smaller scope than the full worktree+gate work, immediately shippable.

## Why

Claude (this conversation, multiple violations on 2026-04-29) keeps making 'tidy-up' commits directly on main instead of in a worktree. Stated discipline alone doesn't hold; the easy path needs to be the right path. Mechanical guard required.

Discussed git pre-commit hooks first; rejected because:
- Per-clone install (manual; not a product feature)
- Not composable (one script per hook type per clone)
- Bash hook is Windows-hostile

Better layer: extend the Claude Code hook surface endless already owns (cmd/endless-hook/claude.go). PreToolUse fires on every Bash call; we add one rule.

## Rule

PreToolUse handler for Bash:
1. Parse the command. If not 'git commit ...', allow.
2. Resolve cwd's repo via 'git rev-parse --git-common-dir' (E-972's pattern). If cwd is in a worktree (.git is a file, not a directory), allow.
3. If cwd is main's working tree AND there's no .git/MERGE_MSG (i.e., not in the middle of a merge), deny with an actionable message:

   'Direct commits to main are not allowed. Make changes in a worktree (.endless/worktrees/<id>/) on a per-task branch, then ff-merge. See endless worktree land (E-971) when shipped.'

4. Otherwise allow.

Returns deny via Claude Code hook response; the rejection appears in Claude's context, not silently in stderr. No --no-verify equivalent in Claude Code hooks.

## Composes with E-971

The W3 design (E-971) puts the behavioral gate (UserPromptSubmit Layer 1/2) and worktree-task match check in PreToolUse. This rule is another PreToolUse check in the same 'discipline subsystem'. Could fold into E-971 but standalone is cleaner because:
- Doesn't depend on per-session worktree creation existing
- Doesn't depend on plan-bearing detection
- Just requires recognizing 'git commit' in Bash payload and checking cwd
- Can ship before E-971 lands and start enforcing immediately

## Critical files

- cmd/endless-hook/claude.go - extend handlePreToolUse with a Bash-command branch that detects git commit; helper to detect worktree-vs-main using 'git rev-parse --git-common-dir' or filepath.IsDir on cwd's .git.

## Acceptance criteria

(a) From main checkout, Claude bash 'git commit -m foo' is denied with the actionable message.
(b) From inside a worktree, 'git commit -m foo' succeeds.
(c) From main during an active merge (.git/MERGE_MSG present), 'git commit' is allowed (so 'git merge' completing a non-ff merge isn't blocked).
(d) Non-commit git commands ('git status', 'git diff', etc.) are unaffected.

## Cross-platform note

Hook binary is Go; builds for Windows/macOS/Linux. The git commands invoked ('git rev-parse --git-common-dir') are portable. No bash-specific logic.