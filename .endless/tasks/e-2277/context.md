Seen 2026-10-08 right after landing E-2275: `endless task unsettled 2275`
reported `Verdict: unlanded (1 commit)` and "Fix: endless worktree land
E-2275" although the commit (14f570d55) was already an ancestor of main. A
minute later the same command reported `settled`.

Cause: the unlanded cache (`internal/monitor/unlanded_cache.go`, E-2128)
stores an unsettled verdict under `unsettled/<base-tip>/<branch-tip>`, and
`cachedUnlanded` keys the read on the watermark's base tip (`wm.tip`), not the
live base tip. Only the background job advances the watermark, so between a
land's ff-merge and the job's next sweep the pre-land "unsettled" entry is
still reachable. The comment on `computeUnlandedAndCache` accepts this because
a stale unsettled hit only over-reports, which is safe for the reaper — but
`task unsettled` is a direct question, asked most often right after a land, and
it answers wrong with a wrong remedy (re-land).

Candidate fixes: on the compute-on-miss path, trust an unsettled hit only when
the watermark tip equals the live base tip (one `git rev-parse`), else
recompute; or have `worktree land` advance the watermark after its ff-merge.
