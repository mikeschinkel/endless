# Pre-rebase check in `worktree land`: abort with a named-files error when `.endless/` is dirty

## Problem

`endless worktree land` invokes `git rebase` without first checking for
uncommitted or untracked files under `.endless/`. When the working tree
is dirty, rebase fails partway with a generic "You have unstaged changes"
error — no file list, no recovery hint.

## Why this is a safety net, not the primary fix

The original failure (snapshot files left untracked by `task update
--status verify`) is being resolved structurally by E-1361 / E-1362 /
E-1363 / E-1364, which move plan snapshots and verbs.json out of the
working tree into `<main>/.git/info/endless/`. After those land, the
snapshot writer no longer dirties `.endless/`.

This task adds a check in `worktree land` that catches any OTHER
`.endless/` writer that might leave the tree dirty — a future writer
added but missed during the E-1361 rollout, a manual edit, a
partially-staged plan file. Instead of git's generic error, the user
sees the file list and the fix.

## Scope

In `endless worktree land`, before invoking `git rebase`:

1. Run `git status --porcelain` on the worktree.
2. If any uncommitted (modified, staged, or untracked) entries exist
   under `.endless/`:
   - Abort the land with an error that lists the files AND names the fix
     command (e.g.
     `git add <files> && git commit -m "Endless: record ledger entry"`).
   - Do NOT auto-commit.
3. Uncommitted entries outside `.endless/` retain today's behavior
   (rebase surfaces its own message).

## Out of scope

- Commit-at-write-site for snapshots and verbs.json — resolved by E-1361.
- Auto-committing dirty files inside `worktree land` — rejected;
  attribution belongs with the writing operation, not with land.
- Detecting dirty trees outside `worktree land`.

## Verification

1. From a worktree, create an untracked file under `.endless/`
   (e.g. `touch .endless/scratch.txt`).
2. Run `endless worktree land <id>`.
3. Land must abort BEFORE rebase, with an error message that:
   - Names `.endless/scratch.txt` explicitly.
   - Shows the commit command to fix.
4. Commit the file manually, re-run land — must succeed.
5. Repeat with a modified-but-unstaged `.endless/` file — same behavior.
6. Untracked file outside `.endless/` — land does NOT abort early;
   rebase's native handling proceeds.
