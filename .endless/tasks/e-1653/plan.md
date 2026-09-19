# E-1653: Fix tasks stranded on old status slugs (no-compromise, no-footgun)

## Context

E-1252 renamed the task-status vocabulary in **code + docs only** —
`needs_plan→unplanned`, `in_progress→underway`, `verify→unverified` — and
deliberately left the **data** and the **immutable ledger** on the old slugs.
The new code no longer recognizes the old slugs, so **424 live tasks**
(330 `needs_plan` + 19 `in_progress` + 75 `verify`) fall out of the queries that
compute "active"/filters (e.g. `internal/monitor/task.go:29` and
`internal/web/queries.go` select `status IN ('unplanned','ready','underway')`).
Result: `endless task active`, `next`, and `list --status` under-report.

The original E-1653 plan (2026-06-25) also proposed **rewriting the committed
ledger** (perl) plus a cross-worktree pre-land alignment script. That is
superseded by **E-1652** (completed 2026-06-28), whose locked design is: never
rewrite the committed ledger — map old values → new at **replay** time via
upcasting. E-1652 notes the codebase **already does ad-hoc upcasting** for
decisions (`mapLegacyDecisionStatus`) and that E-1671 (the full pipeline) will
**consolidate such shims as seed entries**.

**Decisions locked with Mike this session:**
- Do **not** rewrite the ledger — not even once ("dormant drift is a footgun").
- Instead close the footgun with a small **projector shim** (the E-1652-endorsed
  ad-hoc seed entry), so a future `rebuild-db`/`validate-db` maps old→new and
  live == replayed. No rewrite, no drift.
- Add the `unapproved` status now — **Python lists only**; web UI is not a
  priority (its status surfaces stay for E-1648, which owns the approval workflow).

Scope note: this does **not** make `rebuild-db` safe — it independently drops
~233 tasks (E-1654's job). The shim closes only the *status-revert* footgun.

## Change 1 — Live-DB projection fix (the immediate unstranding)

New change-file `internal/schema/changes/e-1653-migrate-status-data.sql`
(named for E-1653, the task doing it — the plan's `e-1252-…` name is wrong by
convention). Header comment documents that this is **projection-only** (ledger
intentionally untouched; the projector shim in Change 2 handles replay):

```sql
UPDATE tasks SET status = CASE status
    WHEN 'needs_plan'  THEN 'unplanned'
    WHEN 'in_progress' THEN 'underway'
    WHEN 'verify'      THEN 'unverified'
END
WHERE status IN ('needs_plan', 'in_progress', 'verify');
```

`blocked` intentionally untouched (E-1532 drops it). Apply to the **live** DB:

1. `endless db backup --db main`  (safety net first)
2. `endless db apply-change internal/schema/changes/e-1653-migrate-status-data.sql --db main`

`apply-change` wraps the file in `BEGIN IMMEDIATE`, records a `_schema_version`
marker, and is idempotent — so re-applying at land time (`just land` drives
change-files) is a no-op. The sandbox never applies change-files (it builds from
`schema.sql`), so this only ever runs against the real DB.

## Change 2 — Projector shim `mapLegacyTaskStatus` (closes the footgun)

Mirror the existing `mapLegacyDecisionStatus` (`internal/events/decision.go:262`).
Add to `internal/events/projector.go` a **pass-through-default** mapper:

```go
func mapLegacyTaskStatus(legacy string) string {
	switch legacy {
	case "needs_plan":
		return "unplanned"
	case "in_progress":
		return "underway"
	case "verify":
		return "unverified"
	default:
		return legacy   // open status set — pass through unchanged
	}
}
```

(Default must pass through — unlike the decision mapper — because task statuses
are an open set; only the three retired slugs are remapped. `blocked`,
`completed`, `declined`, `obsolete` etc. flow untouched.)

Insert a call at the **four** replay handlers that write `tasks.status` from the
payload (map into a local *before* the SQL write and before any terminal-status
check that reads the value):

- `replayTaskCreated` — wrap `p.Status` (~line 178). (Genuine tasks only;
  `type=="decision"` already routes away at ~133.)
- `replayTaskStatusChanged` — map `p.NewStatus` into a local right after
  `taskID := …` (~line 238); reused at the terminal check (~242), cascade
  UPDATE (~255), and single-row UPDATE (~263).
- `replayTaskFieldsUpdated` — map the `status` field value where appended
  (~368-369) and feed the mapped value into the terminal check (~372-390).
- `replayEpicStatusDerived` — wrap `p.NewStatus` (~line 525).

`replayTaskImported` needs no change (hardcodes `'unplanned'`, ignores payload).
`task.claimed`/`task.released` carry no status.

**Tests** in `internal/events/projector_test.go` (extend the existing
`TestProjectToTempDB_TaskCreatedProducesRow` analog at line 34): add cases that
feed a legacy slug through each of the four paths and assert the projected
`tasks.status` is the new slug. Add an epic case near
`epic_derivation_test.go:287`.

## Change 3 — Add `unapproved` to the valid status set (Python only)

Insert `unapproved` right after `unplanned` (its workflow position: unplanned →
unapproved → ready) in both:
- `src/endless/cli.py:121-123` — `TASK_STATUSES`.
- `src/endless/task_cmd.py:3295-3297` — the `valid` tuple.

Do **not** touch terminal/reopenable sets or the active-task queries — `unapproved`
is a pre-`ready` gate with no rows and no `approve` verb yet; E-1648 wires the
workflow. Do **not** touch the Go web surfaces (`internal/web/handlers.go` valid
map, `status_detail.templ` `statusChoices`) — web UI deferred per Mike.

## Files

- `internal/schema/changes/e-1653-migrate-status-data.sql` (new)
- `internal/events/projector.go` (add `mapLegacyTaskStatus` + 4 call sites)
- `internal/events/projector_test.go` (+ `epic_derivation_test.go`) — shim tests
- `src/endless/cli.py`, `src/endless/task_cmd.py` — add `unapproved`
- `tests/tasks/e-1653-verify.sh` (new) — per-task verify script (sandbox-side)

## Build & test

- `just build` (regenerates templ, builds Go into `bin/`) — required so the
  worktree binary reflects the projector change.
- `go test ./internal/events/...` — new shim cases pass.
- `just test` — Python suite green (check nothing asserts `len(TASK_STATUSES)`;
  `tests/tasks/e-1252-verify.sh` should still pass).

## Verification (end to end)

1. **Live unstranding** — `endless sql "SELECT status,COUNT(*) FROM tasks GROUP BY status" --db main`:
   zero `needs_plan`/`in_progress`/`verify`; `unplanned`/`underway`/`unverified`
   up by 330/19/75; total still **1002**.
2. `endless task active --db main` now returns the real in-progress/verify set,
   not the undercount.
3. **Footgun disarmed** — `go test ./internal/events/...`: replaying a legacy
   slug through each of the 4 paths yields the new slug (live == replayed on the
   status axis; no rewrite of the ledger).
4. **`unapproved` accepted** — in the sandbox (don't pollute main):
   `endless task update <id> --status unapproved --db sandbox` succeeds.
5. Backup exists under `~/.config/endless/backups/`.

## Landing

Commit all changes to the worktree branch. Set `endless task update E-1653
--status unverified --db main`. Spawning session (E-1687) owns `just land E-1653`;
the change-file re-applies as a no-op via its `_schema_version` marker. No ledger
rewrite ⇒ no cross-worktree rebase conflict surface.
