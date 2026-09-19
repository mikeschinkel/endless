Companion to E-1012 (Claude Code hook for blocking commits-on-main). E-1012 catches Claude's bash invocations of 'git commit'. This task captures the deferred broader-scope option: git hooks for catching the USER's direct 'git commit' on main (or any other tool that bypasses the Claude Code hook layer).

## Why deferred

E-1012 covers the actual current problem (Claude's discipline failures). Git hooks add coverage for:
- User typing 'git commit' directly in their terminal
- Other tools (IDE git integrations, scripts, sibling AI agents not using Claude Code) committing
- CI bots running outside the Claude Code surface

These aren't current pain points. Filing now to keep the option visible; not for execution until E-1012 ships and we identify a real case the Claude Code layer doesn't catch.

## Challenges to design through (when revisited)

(1) **Distribution**. Git hooks live in .git/hooks/, NOT in the repo. Requires installation step. Endless can manage this via 'endless setup' (already installs Claude Code hooks); add an option to install/update git hooks.

(2) **Composability**. Only one script per hook type per clone. If user already has a pre-commit hook (linter, format check, conventional-commits validator, etc.), endless's hook fights it. Possible solutions:
  - Hook-runner pattern: endless's hook delegates to a chain of sub-hooks (the user's existing hook plus endless's rule). Pre-commit (the Python tool, https://pre-commit.com) does this.
  - Detect existing hook on install and prepend/append our logic.
  - Use core.hooksPath to a directory we manage; user's existing hooks moved into it.
  None are great.

(3) **Cross-platform**. Bash hooks are Windows-hostile. Options:
  - Write hooks in Go, ship as endless-hook-precommit binary, hook is a one-line wrapper invoking the binary.
  - Skip Windows; document it.
  Tmux is also non-Windows, so endless's primary surface today is Unix-y; Windows may be deferable.

(4) **Bypass**. 'git commit --no-verify' silently bypasses. Pre-commit hooks can't prevent --no-verify. Documented as a deliberate user choice; live with it.

(5) **Worktree handling**. The hook needs to detect cwd is in a worktree (allow) vs. main (deny). 'git rev-parse --git-dir' returns the absolute .git path; if it's '<repo>/.git/worktrees/<name>' it's a worktree, otherwise main. Portable.

## Likely approach (sketch, not commitment)

Endless ships a Go binary 'endless-git-precommit'. 'endless setup --install-git-hooks' writes a one-line wrapper at .git/hooks/pre-commit (and other relevant hooks) that invokes the binary. Wrapper is overwriteable with --force. If user has an existing hook, --install-git-hooks --merge appends to it (keep both behaviors).

The binary:
1. Read git env to find branch, .git location.
2. If branch != main, exit 0.
3. If a merge is in progress (MERGE_MSG present), exit 0.
4. If cwd is in a worktree (per .git path), exit 0.
5. Otherwise print actionable error and exit 1.

Same logic as E-1012's Claude Code hook; different invocation surface.

## When to revisit

When any of:
- A real incident shows main was dirtied by something Claude Code didn't catch.
- Endless has non-Claude users.
- A user requests it.