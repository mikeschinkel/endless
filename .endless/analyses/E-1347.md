## Reopened 2026-08-20 — the fix inverted the failure mode rather than removing it

E-1347 shipped `git update-index --skip-worktree .claude/settings.json` because
the *tracked-modified* file blocked rebase during `worktree land` (hit landing
E-1342). That remedy trades one rebase blocker for the opposite one: skip-worktree
tells git the file must not be touched, so git now refuses to check out any
commit that CHANGES it.

Same command, same file, same land step, mirrored trigger:

| | before E-1347 | after E-1347 |
|---|---|---|
| blocked when | the worktree modified the file | **main** modifies the file |
| git says | local changes would be overwritten | local changes would be overwritten |
| how often | every worktree with a sandbox bind | every live worktree, simultaneously |

The second row is worse in blast radius. The pre-E-1347 failure was per-worktree
and self-inflicted; this one fires in *every* self-dev worktree alive at the
moment a commit touching `.claude/settings.json` lands on main, and the developer
who is blocked never edited that file.

## Evidence — the E-1997 land (folded from E-1998)

`just land` for E-1997 failed with:

```
Error: rebase conflict while rebasing your branch onto main.

Conflicting files:
  (none reported)
```

The empty file list is the tell. The real git failure was upstream of any
conflict:

```
error: Your local changes to the following files would be overwritten by checkout:
	.claude/settings.json
Please commit your changes or stash them before you switch branches.
Aborting
error: could not detach HEAD
```

`git ls-files -v .claude/settings.json` in the worktree reported `S`
(skip-worktree). Main had advanced with commit `029208d9`, adding
`"autoMemoryEnabled": false` to the tracked `.claude/settings.json`. No commit on
the branch was involved; the rebase never started.

Manual recovery that worked:

```sh
git update-index --no-skip-worktree .claude/settings.json
git checkout -- .claude/settings.json
git rebase main
```

Deliberately NOT followed by `just claude-settings-init`, since regenerating the
file re-arms the collision for the next land.

## Design tension to resolve (not a decision)

E-998 wants the regenerated per-worktree override invisible to `git status`;
landing wants the file checkout-able. skip-worktree buys the first at the cost of
the second, and E-1347 is where that trade was made. Options worth weighing:

- Clear skip-worktree for the duration of the rebase inside `worktree land` and
  restore it after. Narrow; preserves E-998's contract; fixes only the land path.
- Stop tracking a per-worktree `.claude/settings.json` at all — leave the
  committed file alone and put the override somewhere git does not track. Removes
  the tension instead of timing around it.
- `--assume-unchanged` instead of `--skip-worktree`. Different semantics; verify
  whether git also refuses the checkout before assuming it helps.

## Scope

self_dev only. `claude-settings-init` refuses to run from the main checkout and
is not part of a downstream project's worktree bootstrap, so a project that
merely *uses* endless never sets the bit and never sees this.

## Related, deliberately not merged here

The misleading "rebase conflict / (none reported)" wrapper and its `reset --hard`
recovery suggestion belong to E-1957 (and E-1272 for the destructive hint). Those
mislabel *any* non-conflict rebase failure; fixing this task removes one trigger,
not the misclassification.

## The unnamed option above has a name: `.claude/settings.local.json` (2026-09-08, with Mike)

The second option — "put the override somewhere git does not track" — is not
hypothetical machinery. Claude Code already provides the file for it, and this
repo already gitignores it (the `settings.local.json` rule in `.gitignore`,
which the comment block there says is "exclusively Claude's local-settings
file"). It sits at Local tier, ABOVE project settings in precedence, and
Claude Code merges it natively.

Writing the generated per-worktree override there instead of overwriting the
tracked `.claude/settings.json` removes the collision outright rather than
timing around it: nothing is tracked, so nothing needs skip-worktree, so no
rebase can ever be refused over it. The committed `settings.json` stays exactly
as it is, carrying `enabledPlugins` for everyone, and main can change it freely.

### E-998's two reasons for rejecting it do not hold

Mike concurred with this assessment on 2026-09-08.

1. *"Single owner per file"* — the claim was that `settings.local.json` is
   hand-edited by the user for `permissions`, so writing there would force a
   read-merge-write. But `claude-settings-init` ALREADY does a read-merge-write
   on `settings.json` — that is exactly how it preserves `enabledPlugins` while
   replacing the hooks block. The complexity is identical and already paid; it
   would just be paid against a different file.

2. *"Moving the override to settings.local.json would not stop the user-level
   hook from firing"* — true, and irrelevant here. That addresses E-998's
   hook-DUPLICATION goal. It is not an argument that the file fails at the
   tracking problem, which is what this task is about. Two different problems
   were answered with one rejection.

### Why this is not the sandbox

The generated override must sit where Claude Code discovers settings — the
project root's `.claude/`. Claude Code will not read it from a sandbox
directory, so relocating it there cannot work without a symlink. That makes
this file NOT an instance of the "test-time files belong in the sandbox"
principle (E-2035/E-2050); it is its own, simpler fix. Keeping the two separate
matters, because folding this in as a sandbox case would attach the wrong
remedy to it.

### Measured cost of leaving it alone

2026-09-08: 128 of 132 worktrees on this machine carry the skip-worktree bit on
`.claude/settings.json`. A single main-checkout commit touching that file
blocks the rebase in every one of them simultaneously, each reporting a failure
that names no conflicting file. It has now happened at least three times
(landing E-1997, landing E-2107's follow-up E-2122, and the E-1733 worktree).

### Verify before implementing

Confirm empirically that Claude Code merges `settings.local.json` over
`settings.json` for the `hooks` and `env` keys specifically — E-998's plan
asserted merge semantics without testing them, and this task should not repeat
that. A throwaway project with both files and a trivial hook settles it.
