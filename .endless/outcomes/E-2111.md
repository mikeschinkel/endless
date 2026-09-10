# What the brainstorm determined

## The framing was one problem wearing two costumes

The task was filed about the reaper: an exact `git range-diff` probe on
PreToolUse/PostToolUse cost ~90s per sweep and stopped the product, so it was
reverted to the cheap SHA check and its known leak. Measurement during the
brainstorm found the larger load elsewhere — the DISPLAY path, where every
`session monitor` pane re-probes every rendered row every two seconds with no
sharing between processes, keeping 4-6 range-diffs in flight at every sampled
instant.

Those are one cause, not two: **the exact verdict is recomputed from scratch by
every consumer on every tick, with no shared derived state.** Under ED-1550 that
is one task. Accepted as ED-1589; implemented by E-2128.

## The invariant that makes exactness affordable

The unlanded set can only SHRINK as the base gains commits. The probe asks, per
branch commit since the fork, whether a matching commit exists on the base since
the fork; adding commits to the base only adds candidates to match against, so a
commit that matched keeps its match and one that did not may acquire one.

Therefore a verdict of ZERO stays true as long as the BRANCH tip does not move,
however far the base advances — which is the majority of worktrees and exactly
the population the reaper cares about. A verdict of N>0 is valid only while both
tips hold.

This collapsed most of the options in the seed analysis into one. The
cold-cache-after-a-land objection to caching largely evaporates, because a land
invalidates only the worktrees that were already unsettled.

The append-only premise it rests on is CHECKED, not assumed: a base-tip
watermark plus `git merge-base --is-ancestor` detects an amended, reset or
force-pushed base and flushes. An amend on a task BRANCH moves that branch's tip
and self-invalidates one entry. Two cases deliberately not covered, both
pre-existing: work that landed and was later reverted still reads as settled,
and uncommitted or untracked files are not commits at all — a separate probe
that stays live.

## Measurements that decided the design

- `git worktree list --porcelain` returns every worktree's HEAD **and** the base
  tip in **14ms for 135 worktrees**. One git call is the entire validity check
  for a whole repo, which is what makes a cache-only display path essentially
  free.
- `git range-diff` costs **584ms** for a worktree 648 commits behind the base,
  and 2.1s for one 2251 behind. The cost tracks DRIFT, not what the branch
  holds.
- `git status --porcelain` costs **15-27ms** per worktree. Three orders of
  magnitude below range-diff, so keeping the dirty check live is affordable and
  it has no honest cache key anyway.

## Decisions taken

1. **One writer, many readers.** A background job computes the verdict; the
   display path reads and never computes, rendering a fourth marker state when
   no valid entry exists. This is the seam the planned move away from tmux needs
   regardless, so it is infrastructure rather than an optimisation.
2. **The reaper comes off the hook path entirely** and becomes a second job on
   an hourly cadence, triggered by the existing `liveview` job runner. A
   destructive sweep does not belong before and after every tool call at any
   price. Accepted cost: with no monitor running anywhere, nothing is reclaimed
   until one is.
3. **No file format.** The cache is content-addressed paths under the git common
   dir — an empty `settled/<branch-tip-oid>` marker, an
   `unsettled/<base-tip-oid>/<branch-tip-oid>` file holding the commit lines,
   and one 41-byte watermark. Base movement makes stale entries unreachable, so
   invalidation is a path miss requiring no logic and no stored field. Nothing
   can drift out of sync because every key IS a git object id.
4. **Not the database.** A new runtime table would have been legitimate (`jobs`,
   `errors`, `triage_claims` are written directly and never enter the ledger),
   but E-2087 deliberately made this probe DB-free and `_unsettled_probe`
   documents what that buys: `--db main` from inside a worktree and a bare run
   agree by construction. A verdict in SQLite breaks it — from a worktree the
   read resolves the per-worktree sandbox and finds nothing. The tasks table is
   doubly wrong: `unsettled_item` explicitly serves worktrees with no task row,
   and `tasks` is ledger-projected, so a per-minute verdict would write
   permanent churn into the durable record.
5. **Keeping worktrees in sync needs BOTH directions.** E-1881 (worktree ->
   main) is what lets a worktree settle and become reclaimable; it does nothing
   for probe cost. E-2129 (main -> idle worktrees) collapses the drift the probe
   pays for; it settles nothing. E-1881 is a prerequisite, not an alternative:
   without it a rebase hits the overlapping-metadata conflicts E-1882 exists to
   resolve by hand.

## Directions weighed and set aside

- **A cheaper inexact algorithm.** `git cherry` is ~3x faster but misses
  conflict-resolved landings; subject-plus-author-date pairing is ~35x faster but
  disagrees with range-diff in BOTH directions. Neither is safe alone, and once
  exactness is cached the trade stops being necessary.
- **A shared per-repo patch-id index** (E-2127, `maybe`). Real, and it would cut
  a cold miss roughly tenfold — but every miss is now paid by a background job
  off any critical path, and settled verdicts survive base movement, so
  cold-miss storms are small. Measure before building.
- **Fixing the landing-recording gap.** The seed offered it as possibly the root
  cause. It is a fourth symptom: once exactness is cheap, unreliable
  `task_landings` rows stop being load-bearing for this question. Worth doing for
  the ledger's own sake, not to make this correct.

## Loose end left deliberately

E-2113 (a probe killed by a signal recorded as a fault) is unaffected. The job
runs on a goroutine inside the monitor process, so Ctrl-C still kills in-flight
git children. This work shrinks the window enormously — the probe stops running
continuously — but does not close it. Already filed; not folded in.
