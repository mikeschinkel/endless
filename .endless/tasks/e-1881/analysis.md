## One operation, two steps, fixed order

Absorbs E-2129. The two directions were filed as separate tasks with a blocker
between them; that blocker was not a dependency between two features but step
ordering inside one algorithm, which is what made them one task (ED-1550).

Per worktree, in this order:

1. **Up — endless-managed commits to main.** Sync the `endless-auto` commits
   touching only `.endless/plans`, `.endless/analyses` and `.endless/db-ledger`
   from the worktree branch to main. Never code, tests, or any non-`.endless`
   file: those stay gated by the normal land workflow.
2. **Down — rebase the worktree onto main.** Moves the merge base forward.

Neither step subsumes the other, and each does something the other cannot:

- Step 1 is what lets a long-lived worktree SETTLE and become reclaimable.
  Those metadata commits are the bulk of what makes a branch read as holding
  unlanded work. It does nothing for probe cost; by growing main faster it
  slightly increases the cost for every other worktree.
- Step 2 is what removes the probe's cost at the source, and it settles nothing.

## Why the order is not optional

Without step 1 first, the branch still holds metadata commits that main changed
independently, so the rebase hits exactly the overlapping-edit conflicts
E-1882 (`endless worktree reconcile`) exists to resolve by hand — the scenario
that cost ~20 minutes of manual investigation during the E-1537 land. Step 1
running first leaves little on the branch to conflict with, which is what makes
an unattended rebase safe.

## The cost model step 2 exploits

The exact unlanded probe (`unlandedCommits`, via `git range-diff`) costs in
proportion to how far the base has drifted since the fork — the patch-ids it
computes are for every commit the BASE gained, not for what the branch holds.
Measured on this repo: a worktree 2251 commits behind costs 2.1s; e-1077, 648
behind, costs 584ms. A branch sitting at the base tip has an empty base range
and costs almost nothing.

So step 2 does not make the algorithm cheaper — it removes the input that makes
the algorithm expensive.

## Hard constraint on step 2

Only worktrees with NO live session and no process holding cwd inside, via the
same `WorktreeInUse` predicate the reaper shares with the other destructive
path (E-1947). A rebase rewrites history underneath whoever is sitting in the
directory. This is not a tunable.

Step 1 has no such constraint — it reads commits from the branch and writes to
main, leaving the worktree untouched.

## Re-examine E-1882 once this lands

E-1882 (`endless worktree reconcile`) was filed to resolve drift AFTER it
accumulates, by classifying each unlanded commit and telling the operator what
would happen before doing anything destructive. If this job runs continuously,
the drift it resolves largely stops accumulating, and its remaining scope
shrinks to the cases this job cannot touch — worktrees held by a live session,
and branches whose metadata commits genuinely diverge rather than duplicate.

That may leave E-1882 worth keeping as a narrower manual escape hatch, worth
folding into this job's failure path, or obsolete. Decide it with measurements
after this lands, not before: the question is how often a land still hits a
rebase conflict once this job is running.

## Interaction with E-2128

Both steps move the branch tip, invalidating that worktree's cached unlanded
answer. They compose rather than fight — the cache is content-addressed and
self-invalidating, so there is nothing to coordinate, and the recompute is
cheap by construction because the drift it would have measured is exactly what
step 2 removed.

## Open mechanism question for the planning pass

Rebase versus merge is E-1109's config preference. Rebase matches what
`worktree land` already does; a merge is gentler but leaves a merge commit on
every task branch.
