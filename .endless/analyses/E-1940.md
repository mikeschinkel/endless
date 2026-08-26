# Scope extension — the false-UNLANDED case (added 2026-08-10)

E-1940 covers two defects in `UnsettledDetail.Unsettled()`. There is a third,
opposite one, in the same function, and it has a second consumer.

## The defect

`internal/monitor/worktree_unsettled.go:172` runs
`git rev-list main..HEAD --count` and treats any non-zero result as unlanded.
Because `worktree land` REBASES onto main, a landed branch's commits are
rewritten — the originals stay on the branch and are unreachable from main by
construction. So every correctly-landed worktree reads "unlanded" forever, and
the count grows without bound as main advances.

Observed on E-1845: `task unsettled` reported "unlanded (161 commits)" for a
task that landed on 2026-08-05. Its commit IS on main as 2776f5a5 (same subject,
different SHA from the branch's ed41f1d2); the branch's merge-base with main is
1005 commits back. Most of the 161 were never E-1845's work at all — they were
main's commits at worktree-creation time, since rewritten.

This is worse than a wrong label: the printed fix is
`endless worktree land E-NNNN`, which would replay 161 stale commits onto a main
that already has the content.

## Second consumer — likely the worktree-backlog cause

`internal/monitor/reap_worktrees.go:220` runs the identical probe as reap
criterion 4. Criterion 1 already requires a `task_landings` row. So a landed
worktree passes 1 and is rejected by 4 — it can never be reaped, and landed
worktrees accumulate indefinitely.

## The fix

`task_landings` already records the landing (task_id, merge_commit_sha,
landed_at) and is already read by the reaper. Credit it: the commits a recorded
landing accounts for are not unlanded, whatever the branch's SHA divergence
says.

Note that `git branch --merged <base>` does NOT work here — a rebase-landed
branch is not an ancestor of the base. E-1308 proposes that probe for a related
case and would hit the same wall; the recorded landing is the reliable signal
for both.

### Anchor the credit on the BRANCH, not on the base (revised 2026-08-25)

This section originally said: a task whose recorded sha is reachable from main
is landed. Measurement against this repo falsified that before implementation.

- Most recorded landing shas ARE reachable from main, but a substantial
  minority are not — main's own history has been rewritten more than once, and
  a rewrite detaches every landing recorded before it.
- E-1889, the case this analysis was written from, is one of them. NONE of its
  recorded shas is reachable from main, so the reachable-from-main rule would
  have left the motivating symptom exactly as broken as it found it.
- Every affected branch, without exception, still reaches its own recorded
  landing sha. The branch is the durable anchor; the base is not.

So the primitive is: **unlanded = the commits reachable from HEAD that are
reachable neither from the base branch nor from any recorded landing.**

    git rev-list --count --ignore-missing HEAD ^<base> ^<landing>...

Excluding the base as well as the landings is what keeps the count honest when
the base has moved on: it counts only commits that are genuinely this branch's
and genuinely not in. `--ignore-missing` covers a landing sha that is no longer
an object in this clone; it cannot mask a bad base, because the resolver only
ever returns a branch it verified exists.

Applied to the affected worktrees, every false count collapsed to zero while
the branches carrying real post-landing commits kept theirs.

Verify with a rebase-landed worktree: `unsettled` must report settled, the ◆
marker must clear, and the reaper must consider it reapable. Verify the
distinction that motivated the revision by rewriting the base's history
afterwards — the verdict must not move.


# Consolidated from E-1308 (declined 2026-08-10)

E-1308 was a third symptom of this same cause. Its case: `worktree land E-NNNN`
when the worktree is GONE reports "No endless-managed worktree for E-NNN",
implying the work is lost. It isn't — the branch landed and the reaper later
removed the directory and branch once the `task_landings` row aged past
worktree_ttl.

Required behavior, carried over verbatim: report
"E-NNN already landed (commit <sha>); nothing to do" instead of the error, and
fall back to today's message only when the work is genuinely unaccounted for.

E-1308 proposed detecting this with `git branch --merged main`. That does not
work: `land` rebases, so a landed branch is not an ancestor of main and the
probe fails for exactly the case it targets. The recorded landing is the only
reliable signal — the same fix the unsettled probe and the reaper need.

## The three consumers, one primitive

| Consumer | Current probe | Symptom |
|---|---|---|
| `worktree_unsettled.go:172` | `rev-list main..HEAD --count` | false "unlanded (N commits)" forever after landing |
| `reap_worktrees.go:220` | same | landed worktrees never reaped; they accumulate |
| `worktree land` (gone worktree) | none / proposed `branch --merged` | "No endless-managed worktree", implying work lost |

Build the landed-state determination once — the commits a recorded landing
accounts for are not unlanded, regardless of branch SHA divergence — and route
all three through it. See the revision above for the exact primitive.
