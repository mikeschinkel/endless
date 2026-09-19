# Apply the E-1715 historical landing backfill

## Context

E-1715 (research) produced the confident dataset: **177 (task_id, merge_commit_sha) pairs** for
tasks that landed on main before landing-recording became reliable (2026-05-21) and therefore
have no `task_landings` row. This task *performs* that backfill. It is the "do the thing"
follow-up to E-1715's findings — E-1715 only researched and produced the list + a fallback
script; nothing has been emitted.

## Self-unblock: absorb the minimal record-only slice (no E-1486/E-1537 dependency)

E-1479 is the *full* record-only feature (explicit values **plus** git auto-discovery, a
duplicate guard, session-id resolution, dry-run). Only that extra layer is why E-1479 waits on
**E-1486** (the Python→Go DB port supplies the reads for auto-discovery/dedup/session lookup);
**E-1537** (epic task-type) is unrelated to record-only mechanics at all. **This task needs none
of that layer** — E-1715 already resolved exact `(id, sha)` pairs (no discovery), the set is
one-shot and known-clean (no dedup read needed), and historical lands have no live session
(record `session_id` NULL — the column is already nullable). So instead of blocking on the whole
E-1479 → E-1486/E-1537 chain, **E-1719 carries the small, DB-port-independent slice itself**:

1. **Branch nullability** — `task_landings.branch TEXT NOT NULL` → `TEXT`, in `schema.sql` + a
   `internal/schema/changes/*.sql` live-table rebuild (mirror `e-1459-reshape-schema-version.sql`);
   `projector.go replayTaskLanded` + `executor.go execTaskLanded` insert NULL on empty payload
   branch; `internal/monitor/reap_worktrees.go` reads `branch` as `sql.NullString` (required, or
   a NULL branch crashes the reaper). *(This is E-1479's branch-nullability piece, lifted out.)*
2. **Historical timestamp** — `--ts <iso>` on `cmd/endless-event` (→ `kairos.New(parsed,0,nid)`
   instead of `clock.Now()`) + a `ts` passthrough in `event_bridge.emit_event`, so `landed_at`
   is the commit date, not now(). *(E-1479's `--ts` piece, lifted out.)*
3. **Explicit-value emit** — a minimal `--record-only --sha <sha> [--branch ''] [--at <iso>]`
   path in `worktree_cmd.land_worktree` that skips rebase/ff/discovery/dedup/session-resolution
   and emits `task.landed` (system actor, `session_id` NULL, `branch` NULL, `ts` = commit date).

None of the three touches E-1486's Go DB layer. E-1479 then **narrows to the generalization**
(auto-discovery, dedup, `--session-id`) built on top of this slice once E-1486 lands — it extends
this surface rather than introducing it. Keep the flag/emit shapes compatible so E-1479 is an
extension, not a rewrite; **E-1719 `relates_to` E-1479** (coordination, not a hard block).

Do **not** use E-1715's embedded `endless-go event emit` script except as a last resort — it
fabricates `task/<id>-slug` branch names and stamps `landed_at` = now(), both of which the slice
above fixes properly.

## Implementation phases

- **Phase 1 — the record-only slice** (steps 1–3 above). Self-contained; verify with a probe.
- **Phase 2 — apply the backfill** over E-1715's 177 pairs (below).

## Why drive E-1479 explicitly from the E-1715 list (not its auto-discovery)

E-1479's auto-discovery fallback greps `^E-<id>:` commit subjects. Most of this cohort predates
that convention (they use `(E-N)` suffix, `Verb E-N:`, or merge commits), so auto-discovery
would **miss the majority**. E-1715 already resolved every id to its exact landing sha across
all conventions — feed those pairs in with `--sha`.

## Procedure (from the MAIN checkout `~/Projects/endless`)

1. Read the 177-row DATA block from E-1715's outcome (`endless task show E-1715 --outcome --db main`)
   — columns `task_id | merge_commit_sha`.
2. For each pair: `endless worktree land <id> --record-only --sha <sha>`. The Phase-1 slice
   derives `--at` from `git show -s --format=%cI <sha>` (a pure git read — no DB port needed) and
   records `branch` NULL (the branch is gone). Run the whole set once in `--dry-run` first,
   eyeball, then apply.
3. The Phase-1 slice omits the duplicate guard (that read is E-1486-gated in E-1479). Run once on
   this known-clean set; if a re-run is ever needed, pre-filter against
   `SELECT task_id FROM task_landings` via `endless sql` first.

## Decisions baked in

- **E-1108 excluded** — landed (`fb60208d`) then reverted (`b78ce4ad`); net not on main.
- **E-1669 included** — its commit `6603c394` is a verified ancestor of main and was not
  reverted; a landing row records the historical fact that it landed, independent of the task's
  now-`obsolete` status. (Flip only if you'd rather not carry obsolete-task landings.)

## Also adjudicate the 25 review-bucket ids (from E-1715)

14 plan-edit-only + 11 bare-mention-only. For each: check whether the task's code is actually on
main (e.g. `git log main --oneline | grep -i "E-<id>"`, confirm the referenced commit is a real
feature commit and an ancestor of main). Backfill the ones that genuinely landed via the same
`--record-only --sha` path; leave the rest (decisions/orphans/plan-only) alone. The 7 decisions
(D-958/1007/1094/1175/1199/1361/1373) and orphan E-783 are N/A — no task row to attach to.

## Verification

- `endless sql "SELECT count(*) FROM task_landings"` rises by the number applied (~177 + any of
  the 25 that qualified).
- Spot-check the motivating case: `endless task landed 1209` shows a row at `6671bca9` with
  `landed_at` = 2026-05-09 (E-1209's commit date), `branch` NULL.
- `endless-go event validate-db --project-root ~/Projects/endless` clean.

## Out of scope

- The E-1392 `session_landings` table / `endless session landings` CLI (separate open task).
- Changing the normal land flow.
