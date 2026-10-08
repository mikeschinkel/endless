# Plan — a landed task reads settled the moment the land finishes

## What Mike expects

As soon as `worktree land` finishes, `session monitor` shows the session's
task as settled (no unsettled ◆), and `task unsettled` agrees. Today both
show "unlanded (1 commit)" for up to one job interval (about a minute), and
`task unsettled` adds "Fix: endless worktree land", which is wrong.

## Why it happens

The unlanded cache (`internal/monitor/unlanded_cache.go`, E-2128) answers
`settled/<branch-tip>` first, then `unsettled/<watermark-tip>/<branch-tip>`.
Only the background job (`RefreshUnlandedCache`) advances the watermark. An
"unsettled" entry for the rebased branch tip, written before the ff-merge
under the current watermark, stays readable after the land until the job's
next pass. Nothing writes `settled/<branch-tip>` in that window:
- `session monitor` / `session status` read cache-only, so they show the
  stale entry;
- `task unsettled` uses compute-on-miss, but `worktreeUnsettledAt` returns
  any cache hit without computing, so it shows the stale entry too.

Running the job right after a land would not work: main moves every few
seconds from recorder commits, so a full pass recomputes every worktree
(about 584ms each).

## The fix: the land writes the answer it just made true

After the ff-merge, the branch tip is an ancestor of main, so the branch is
settled by definition. No range-diff is needed to know that.

1. **Go — an ancestor check before trusting "unsettled" (compute mode only).**
   In the compute-on-miss path (both `worktreeUnsettledAt` for
   `unlandedComputeOnMiss` and `computeUnlandedAndCache`), when the cache has
   an unsettled hit or a miss, first run
   `git merge-base --is-ancestor HEAD <base>`. If HEAD is an ancestor, the
   verdict is zero unlanded commits: write `settled/<head>` and return
   settled, with no range-diff. Otherwise behave as today.
   - This is exact: an ancestor of the base holds nothing the base lacks.
   - Cost: one cheap git call, only on compute-mode paths and only when the
     answer is not already settled. The reaper gets slightly more correct;
     the monitor's cache-only path is untouched (no extra per-tick calls).
   - Update the comment on `computeUnlandedAndCache` that calls a stale
     unsettled hit an accepted over-report.
2. **Python — land asks once, right after it lands.** After `_record_landing`
   and the "Landed" echo, `land_worktree` runs
   `endless-go session-query worktree-unsettled <worktree_path>` (reuse
   `_unsettled_probe`). With step 1, that writes `settled/<branch-tip>`, which
   the monitor's next tick reads. Best-effort: a failure prints nothing and
   never unwinds the land, because the job corrects it within one interval.
   The probe is the installed `endless-go` on PATH, so in any project it is
   whatever release the user installed; when it is missing or older than this
   fix, the call is a harmless no-op and the job corrects the display as today.
   (Only for Endless landing itself does land rebuild that binary from the
   advanced main first, so E-2277's own land already uses the fix.)

## Any project, any language

Nothing here reads the project's code or build: the check is git
(`merge-base --is-ancestor`, the default branch Endless already resolves,
`master`/`develop`/`trunk` included) plus the cache under `.git/info/`, so a
Rust, Java, TypeScript or Python project behaves identically. Very large
histories pay one ancestry walk, which git's commit-graph keeps cheap, and
still far below the range-diff it replaces.

## Not in scope

A branch whose content landed without being an ancestor (cherry-pick, squash)
can still read unsettled until the next job pass. `worktree land` always
fast-forwards, so it never produces that case. Fixing it would mean a
range-diff on every compute-mode read whenever main has moved, which happens
every few seconds, and that is E-2087's cost regression.

## Tests

- Go (`internal/monitor`), real throwaway repo: write a stale
  `unsettled/<wm-tip>/<head>` entry, fast-forward base to head, then
  - `WorktreeUnsettledDetailAt` reports settled and `settled/<head>` exists;
  - the cache-only reader then reports settled;
  - a stale entry whose head is NOT an ancestor is still returned as-is
    (no range-diff, unchanged behavior).
- Python: a real-repo `land_worktree` (pattern of
  `tests/test_worktree_land_lock_contention.py`) runs the probe on the landed
  worktree after recording, and a failing probe does not fail the land.

## Verify

`.endless/tasks/e-2277/verify.sh`: the tests above as a fail-fast check, plus
an end-to-end check in a throwaway project that sets up the stale entry,
lands, and confirms `endless task unsettled` reports settled straight away,
using the worktree's built `endless-go`.
