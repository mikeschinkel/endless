# Stop reaper from destroying reopened-after-landing worktrees

## Bug

`internal/monitor/reap_worktrees.go:114 maybeReapWorktree` decides
eligibility by comparing `task_landings.landed_at` to `cutoff = now - ttl`.
`landed_at` is a frozen historical event, not an indicator of ongoing
relevance. When a task is reopened after landing and new commits are
pushed to its branch (no new landing yet), the reaper still considers
the worktree stale and destroys it — taking the in-flight work with it.

Symptom observed (E-1434, 2026-06-10): the reaper fired on every Claude
`PreToolUse`/`PostToolUse`/`SessionStart`/`Stop`/`SessionEnd` event
(`internal/hookcmd/claude.go:158,215,221,236,262`) and removed the
worktree between successive tool calls.

## Root cause

The eligibility check is a single signal (`landed_at`) that doesn't
update when the task is reopened, claimed again, or worked on. Every
other touch the system tracks (session claim, status flip, new commit)
is invisible to the reaper.

## Fix

Replace the single-signal cutoff with a multi-signal "any sign of life?"
guard. Reap only when ALL of these hold:

1. **`task_landings` row exists** — keep the existing gate so
   pre-landing orphans stay protected.
2. **Latest activity is past TTL.** Activity = max of:
   - `task_landings.landed_at` (most recent row)
   - `MAX(session_tasks.updated_at) WHERE task_id = ?`
   `session_tasks.updated_at` is upserted by every task.* event with a
   session actor (`internal/events/session_tasks.go:22 upsertSessionTask`),
   so claim / status flip / decision / etc. all advance it.
3. **No unmerged commits on the branch.**
   `git -C <wt> rev-list main..HEAD --count` returns 0.
4. **No active session bound to the task.**
   `SELECT count(*) FROM sessions WHERE active_task_id = ? AND state != 'ended'`
   returns 0. (Belt+suspenders alongside the existing `hasLiveProcessInDir`
   lsof check.)
5. **Working tree is clean.**
   `git -C <wt> status --porcelain` returns empty. If git itself errors
   on the worktree, treat as "in use" — skip rather than destroy.

Any single signal that says "in use" prevents reap. The order above is
also evaluation order; cheap DB queries first, git subprocesses last.

## Files changed

- `internal/monitor/reap_worktrees.go` — extend `maybeReapWorktree`
  with the new guards. Hardcoded `main` for the upstream branch
  (endless convention; consistent with `just land`).
- `internal/monitor/reap_worktrees_test.go` (new or extended) — cover
  the test plan below.

## Test plan

1. **Landed >TTL ago, no recent session activity, clean, no unmerged
   commits, no active session** → reaped. (Existing behavior, unchanged.)
2. **Landed >TTL ago, session_tasks.updated_at within TTL** → not reaped.
   (The bug fix: reopen-then-work case.)
3. **Landed >TTL ago, session_tasks.updated_at also >TTL** → reaped.
   (Post-grace abandonment still works.)
4. **Landed >TTL ago, branch has unmerged commits** → not reaped.
5. **Landed >TTL ago, sessions row exists with `active_task_id = task`
   and `state = 'working'`** → not reaped.
6. **Landed >TTL ago, working tree dirty** → not reaped.
7. **No `task_landings` row** → skipped, regardless of other state.
   (Existing behavior, unchanged.)
8. **`git status` fails inside worktree** → skipped (conservative).

Tests build their own DB and worktree fixtures rather than relying on
the real ledger. The existing reaper tests in the package are the
nearest template.

## Out of scope (file as separate follow-ups)

- `endless worktree list` showing *why* a stale-by-time worktree is
  protected (dirty / unmerged / active-session). Today's manual tooling
  (`endless worktree list/drop/reap`) suffices; the visual hint is a
  follow-up to file once this lands.
- Throttling the reaper at the Claude-hook call sites. The bug here is
  destroy-correctness, not call frequency.

## Verification

1. Build, run `go test ./internal/monitor/...`.
2. Manually simulate E-1434's exact state: a `task_landings` row >TTL
   old, but uncommitted edits / a session pointing at the task.
   `ReapWorktreesForProject` should leave the worktree untouched.
3. Confirm the existing "post-grace abandoned worktree" case still
   reaps by removing all the protective signals from the fixture.
