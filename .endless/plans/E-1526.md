# E-1526: Fix rollover in .endless/db-ledger that exceeds 500 entries

## Context

`internal/events/writer.go` declares `DefaultMaxEventsPerSegment = 10000` on `main`. The Writer's `Append` logic (writer.go:74–78) already rotates correctly when `count >= maxCount`, so the rotation code itself is fine — but with maxCount=10000, no segment will ever rotate at 500 lines.

E-1310 (status `assumed`, May 13 2026) was a one-line constant change to lower this to 500 (matching the ~1MB IDE-friendly target at ~1.1KB per JSONL entry). **The change was authored and committed on `task/1310-reduce-defaultmaxeventspersegment-so` as `443284c`, but never landed into main.** Mike marked the task `assumed` and on the same day did a separate manual hygiene split (`1e3bbfc` on main) of the then-oversized `db-entries-a7f3-000001.jsonl` (1264 lines → 500 / 500 / 264) under the *expectation* that subsequent writes would rotate at 500 — but they never did because the constant change wasn't actually deployed.

Result today: `db-entries-a7f3-000003.jsonl` is 1172 lines / 1.8MB and still growing (latest line was the E-1526 `task.created` event itself).

E-1310's worktree still exists at `.endless/worktrees/e-1310` but is 323 commits behind main and has a stale ledger-sync commit that would conflict with current main. Per Mike's decision, the fix is being re-done in E-1526 rather than rebased from the older branch; E-1310 will be marked `obsolete` with E-1526 cited as the superseding task.

## Fix — two commits in this worktree

### Commit 1: re-apply E-1310's constant change

In `internal/events/writer.go`, lower `DefaultMaxEventsPerSegment` from 10000 to 500 (verbatim diff from `443284c`, including the explanatory comment):

```go
// DefaultMaxEventsPerSegment is the default rotation threshold. Sized so
// each segment stays IDE-loadable when a user needs to open the ledger
// by hand: at ~1.1KB per JSONL entry, 500 entries yields ~550KB
// segments, comfortably under the 1-2MB threshold where JetBrains
// (and many other editors) start to lag.
const DefaultMaxEventsPerSegment = 500
```

No other file changes: the only existing reference to the constant is `writer.go:51` itself, and the only test reference is `SetMaxCount(3)` in `writer_test.go` which is unaffected by the default.

Run `go test ./internal/events/...` and confirm all tests pass.

### Commit 2: hygiene-split `db-entries-a7f3-000003.jsonl`

Mirror Mike's `1e3bbfc` pattern: split the 1172-line / 1.8MB segment into three IDE-loadable pieces, with the highest sequence becoming the new active segment.

Target layout:
- `000003`: first 500 lines (was 1172)
- `000004`: lines 501–1000 (500 lines, new)
- `000005`: lines 1001–N (172+ lines, new — active segment after this commit; "N" because the live file may continue to grow during planning/implementation, so the exact count is captured at split time, not predicted)

Race safety follows the same pattern Mike used in `1e3bbfc`:

1. Snapshot: `cp db-entries-a7f3-000003.jsonl /tmp/000003.snapshot`
2. Capture `wc -l` of both source and snapshot; refuse to proceed if they differ (a write landed mid-snapshot).
3. Write new files from the snapshot (not the live source):
   - `head -n 500 /tmp/000003.snapshot > 000003.new`
   - `sed -n '501,1000p' /tmp/000003.snapshot > 000004.jsonl`
   - `tail -n +1001 /tmp/000003.snapshot > 000005.jsonl`
4. Re-verify the live source line count matches the snapshot (race-check).
5. Atomic swap: `mv 000003.new db-entries-a7f3-000003.jsonl`.
6. Byte-for-byte sanity check: `cat 000003 000004 000005 | diff - /tmp/000003.snapshot` must be empty.
7. `rm /tmp/000003.snapshot`.

After step 5 the Writer's `scanSegments` will pick `000005` as the active segment (highest seq) and rotate to `000006` when count hits 500.

### Sequencing

Both commits must land *together* via `endless worktree land E-1526`. No `endless-go event emit` should run between commit 2 and the land step, because the now-shipped binary (commit 1) would otherwise create a new `000006` while `000005` is below threshold, leaving a gap. The land step is short enough that this is fine in practice; if a write does sneak in, it lands harmlessly in `000005` (still under 500), which is correct.

## After landing: close E-1310

`endless task update E-1310 --status obsolete --outcome "Superseded by E-1526; the constant change in 443284c was never landed, E-1526 re-applied it together with the matching hygiene split of 000003."`

The orphan branch `task/1310-reduce-defaultmaxeventspersegment-so` and its worktree at `.endless/worktrees/e-1310` can be reaped via the standard worktree retention/cleanup flow (no manual `worktree drop` per the E-1332 grace-period policy).

## Files

- `internal/events/writer.go` — `DefaultMaxEventsPerSegment` (writer.go:18–19) and comment.
- `.endless/db-ledger/db-entries-a7f3-000003.jsonl` — truncated to first 500 lines.
- `.endless/db-ledger/db-entries-a7f3-000004.jsonl` — new, 500 lines.
- `.endless/db-ledger/db-entries-a7f3-000005.jsonl` — new, 172+ lines.

## Out of scope

- **Investigation of *why* E-1310 didn't land** (marked `assumed` without `worktree land`). Process concern; mention to Mike but no remediation here.

## Verification

1. `just build` — rebuild `bin/endless-go` with the new constant.
2. `go test ./internal/events/...` — existing tests pass (`TestWriter_Rotation` uses `SetMaxCount(3)` so unaffected by the default).
3. After commit 2: `wc -l .endless/db-ledger/db-entries-a7f3-00000{3,4,5}.jsonl` reports `500`, `500`, `<remainder>`, total matching the pre-split count.
4. `cat .endless/db-ledger/db-entries-a7f3-00000{3,4,5}.jsonl | wc -c` matches `wc -c` of the pre-split snapshot byte-for-byte.
5. After landing E-1526 into main, the next session-driven event emit (e.g. any `endless task` write) appends to `db-entries-a7f3-000005.jsonl`. Once it reaches 500 lines, a subsequent emit creates `db-entries-a7f3-000006.jsonl` — confirming end-to-end rotation under real load.
6. Verify `endless task show E-1310 --db main` reports status `obsolete` with the cited reason.
