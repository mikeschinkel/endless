## What happened

`just land` for E-1997 failed with a rebase error. No commit of the branch was
involved:

```
error: Your local changes to the following files would be overwritten by checkout:
	.claude/settings.json
Please commit your changes or stash them before you switch branches.
Aborting
error: could not detach HEAD
```

`git ls-files -v .claude/settings.json` in the worktree reports `S` —
skip-worktree, set on purpose by `just claude-settings-init` so the regenerated
per-worktree hook override stays out of `git status` (E-998).

skip-worktree tells git the file is unchanged and must not be touched. When a
commit being checked out *does* change it, git refuses rather than clobber it.
Main had advanced with `029208d9 Update for LESSONS.md`, which added
`"autoMemoryEnabled": false` to the tracked `.claude/settings.json`.

## Why it matters beyond one land

The blast radius is every self-dev worktree that is alive when that file changes
on main — they all become unlandable simultaneously, and the failure surfaces as
a rebase error naming a file the developer never edited.

## Recovery used (manual, not discoverable)

```sh
git update-index --no-skip-worktree .claude/settings.json
git checkout -- .claude/settings.json
git rebase main
```

Then deliberately NOT re-running `just claude-settings-init`, because that
re-arms the collision for the next land.

## Design tension to resolve

E-998 wants the regenerated file invisible to `git status`; landing wants the
file checkout-able. skip-worktree buys the first at the cost of the second.
Options worth weighing (not a decision):

- Have `worktree land` clear skip-worktree for the duration of the rebase and
  restore it after — narrow, keeps E-998's contract, fixes the land path only.
- Stop tracking `.claude/settings.json` per-worktree at all: keep the committed
  file untouched and put the override somewhere git does not track.
- `--assume-unchanged` instead of `--skip-worktree` (different semantics; verify
  whether it also refuses the checkout before assuming it helps).

## Modes

self_dev only. `claude-settings-init` refuses to run from the main checkout and
is not part of a downstream project's worktree bootstrap, so a project that
merely *uses* endless never sets skip-worktree and never sees this.
