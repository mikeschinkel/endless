## The convention already exists

`.endless/tasks/e-NNNN/` is already in use — 238 entries today, holding each
task's `verify.sh` beside the shared `_harness.sh`, `_guard.sh` and `CLAUDE.md`.
This consolidates the rest of a task's content into the directory that already
belongs to it, rather than inventing a layout:

```
.endless/tasks/e-2128/plan.md       <- .endless/plans/E-2128.md      (432 files)
.endless/tasks/e-2128/analysis.md   <- .endless/analyses/E-2128.md   (146 files)
.endless/tasks/e-2128/outcome.md    <- .endless/outcomes/E-2128.md   (134 files)
.endless/tasks/e-2128/verify.sh     (already there)
```

Decisions are NOT task-scoped — `ED-NNNN` has no owning task — so
`.endless/decisions/ED-NNNN.md` stays where it is. The restructure is for
task-scoped content only.

## Why now, beyond tidiness

**It fixes an allowlist that has already gone stale once.** E-1881's step 1 has
to name the directories holding task content. Its description, written
2026-08-04, names `plans`, `analyses` and `db-ledger` — and by the time it was
read on 2026-09-14 the real set was plans, analyses, outcomes and decisions,
while db-ledger had become wrong (those commits are orphans to drop, never
content to push up). Every future content type repeats that.

After this, the allowlist is one glob — `.endless/tasks/e-*/*.md` — and a new
content type needs no change to it, ever.

**It fixes a casing split.** `E-2128.md` inside `e-2128/` today. `.endless/tasks/CLAUDE.md`
already warns that a hand-written path "gets that wrong silently on a
case-insensitive filesystem and loudly on everyone else's".

## The two-lifecycle question, and why it is not a problem

Blocked by E-2137 for a reason. With mirrors written to main and not
materialized into worktrees, a worktree's `.endless/tasks/e-NNNN/` holds only
`verify.sh` — branch-authored, lands with the task — while main's holds that
plus the `.md` files Endless wrote there directly.

That is not split-brain, because there is no stale copy in the worktree to
disagree with main. It is one directory that is fuller on main than on a branch,
which is exactly what `.endless/db-ledger/` already is.

Done in the other order it WOULD be split-brain: a stale `plan.md` sitting next
to a live `verify.sh`, with nothing telling a reader which is authoritative.
That is the whole reason for the blocker.

What remains is documentation: `.endless/tasks/CLAUDE.md` must say which files in
that directory are the task's to write and which are Endless's.

## The migration, and what it collides with

A rename on main is a modify/delete conflict for any branch holding a commit at
the old path. Measured 2026-09-14: 51 worktrees hold 316 doc-mirror commits at
old paths, so the conflicts are real — but they are mechanical, and almost all
are the add/add shape where both sides are byte-identical (125 of 134 such files
had identical content on branch and main).

Two things reduce it rather than waiting for a quiet moment that never comes,
since there are always tasks underway:

- E-2137 landing first stops NEW doc-mirror commits reaching branches at all.
- `endless worktree sync` (E-1881) rebasing the fleet afterwards delivers the
  new layout, and a worktree holding no doc-mirror commits rebases cleanly.

The residue is each session resolving one mechanical conflict at its next
rebase, which is what the session-broadcast task exists to make humane.

## Tested: git's directory-rename detection misplaces content across tasks

Measured on a scratch repository 2026-09-14, not assumed. Two shapes, opposite
outcomes:

**A branch that MODIFIES a file main renamed rebases cleanly.** Git follows the
rename and lands the edit at the new path. No conflict.

**A branch that ADDS a file at the old path does not.** With
`.endless/plans/E-0.md` renamed to `.endless/tasks/e-0/plan.md` on main, a branch
adding `.endless/plans/E-2.md` rebased to:

```
.endless/tasks/e-0/E-2.md      <- E-2's plan, inside task e-0's directory
.endless/tasks/e-0/plan.md
.endless/tasks/e-2/plan.md
```

Git's directory-rename detection treated `.endless/plans/` as having become
`.endless/tasks/e-0/` and applied that to every new file in it. It raised a
conflict rather than doing this silently, but that IS the resolution it offers,
and `Endless: add plan for E-N` is the most common commit shape on these
branches.

`.endless/plans/` does not rename to one directory. It fans out to 432, and git
picks whichever one it detected.

**This is why E-2137 blocks this task, and the blocker is load-bearing rather
than an ordering preference.** E-2137's drain removes doc-mirror commits from
branches, so there is no new-file-at-an-old-path left for rename detection to
misplace. A branch that only ever MODIFIED such a file was always safe.

For any branch still holding doc commits when this runs — one whose worktree was
in use during the drain, so it was correctly skipped — the migration must either
re-drain it first or disable rename detection for that rebase
(`-X no-renames`). Do not let git guess: the guess puts one task's content in
another task's directory.
