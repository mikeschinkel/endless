## Why this is the other half, not a duplicate of E-1881

The two sync jobs fix opposite sides of the same subtraction, and neither
subsumes the other.

- **E-1881, worktree -> main.** Moves endless-managed metadata commits (plans,
  analyses, ledger) UP. Those commits are the bulk of what makes a long-lived
  branch read as holding unlanded work, so removing them is what lets a worktree
  SETTLE and become reclaimable. It does nothing for probe cost; by growing main
  faster it slightly increases the cost for every other worktree.
- **This task, main -> worktree.** Rebases an idle branch onto the base, moving
  the merge base forward. That collapses the range the exact unlanded probe
  actually pays for, and it settles nothing by itself.

## The cost model this exploits

The exact probe (`unlandedCommits`, via `git range-diff`) costs in proportion to
how far the base has drifted since the fork — the patch-ids it computes are for
every commit the BASE gained, not for what the branch holds. Measured on this
repo: a worktree 2251 commits behind costs 2.1s; e-1077, 648 commits behind,
costs 584ms. A branch sitting at the base tip has an empty base range and costs
almost nothing.

So this does not make the algorithm cheaper — it removes the input that makes
the algorithm expensive.

## Why E-1881 blocks this rather than merely relating to it

Without E-1881, the branch still holds metadata commits that main changed
independently. Rebasing then hits exactly the overlapping-edit conflicts that
E-1882 (`endless worktree reconcile`) exists to resolve by hand — the scenario
that cost ~20 minutes of manual investigation during the E-1537 land. E-1881
running continuously leaves little on the branch to conflict with, which is what
makes an unattended rebase safe.

## Hard constraint

Only worktrees with NO live session and no process holding cwd inside, via the
same `WorktreeInUse` predicate the reaper shares with the other destructive
path (E-1947). A rebase rewrites history underneath whoever is sitting in the
directory. This is not a tunable.

## Interactions

- **E-2128.** A rebase moves the branch tip, invalidating that worktree's cached
  verdict. They compose rather than fight: the recompute is cheap by
  construction, because the drift it would have measured is what the rebase just
  removed.
- **E-1109** (`merge-vs-rebase` config preference) decides the mechanism. Rebase
  matches what `worktree land` already does; a merge is gentler but leaves merge
  commits on every task branch.
