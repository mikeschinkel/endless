# E-1541 — Implement auto-derivation of epic status from child states

## 0. Decision resolved at pickup (2026-06-21)

The original plan specified the projector two contradictory ways: §4 said replay
the recorded `epic.status_derived` event "without re-running derivation," while
§2/§8-step-6 said *also* run `recomputeEpicStatus` in the projector replay
functions. Doing both double-derives and, more importantly, breaks the
event-sourcing invariant `validator.go` checks (`status` is compared): re-deriving
epics during projection would flag every existing epic whose stored status differs
from its derived value as a `validate-db` mismatch until a `rebuild-db --confirm`
reconciled the live DB.

**Resolution (Mike, Option A): the projector replays recorded `epic.status_derived`
events only; it does NOT run `recomputeEpicStatus`.** The ledger is authoritative —
derived events are recorded on the live path and replayed faithfully, so
`projection(ledger) == live DB` holds and `validate-db` stays clean. Pre-feature
epics are not retroactively derived by a rebuild; derivation applies going forward
and to any epic the moment one of its children changes. The live-path behavior
(recompute + emit derived events) is unaffected by this choice.

## 1. The derivation rule

Epic status is a pure function of the children's statuses, except when the epic itself is in a sticky-override status. Computed top-to-bottom:

| Children's state | Epic status |
|---|---|
| Any child has `status = in_progress` | `in_progress` |
| No `in_progress` but any child has `status = ready` | `ready` |
| No `in_progress` or `ready` and any child has `status = needs_plan` | `needs_plan` |
| All children are terminal (`completed`/`confirmed`/`assumed`/`declined`/`obsolete`) AND at least one child exists | `completed` |
| Epic has zero children | unchanged (no derivation) |

**Sticky-override statuses** block derivation: `revisit`, `declined`, `obsolete`, `blocked`. When the epic is in one of these, derivation reads the current state and does nothing. The override must be cleared (manually set the epic to a derivable status) before derivation resumes.

The "add a non-terminal child to a completed epic reverts it" case from E-1537 §4 is no longer a special case — it falls out naturally: adding a `needs_plan` child to a completed epic makes the epic `needs_plan` again per the rule.

E-1537's §4 will be updated to use this rule.

## 2. Where derivation fires

The recompute fires inline, inside the Go SQL transaction that handles the triggering child mutation. Live-path callers (executor):

- `execTaskStatusChanged` — after the SQL update, before commit
- `execTaskFieldsUpdated` — only when `Fields["status"]` or `Fields["parent_id"]` is present
- `execTaskMoved` — both the old and new parent chains need recompute
- `execTaskCreated` / `execTaskImported` — only when `ParentID != nil`
- `execTaskDeleted` — recompute the deleted task's parent chain (the parent id is captured before the row is removed)

The projector does **not** call recompute (see §0). It replays the recorded
`epic.status_derived` events via `replayEpicStatusDerived`, which reproduces exactly
what the live path did.

## 3. Where the code lives

New file `internal/events/epic_derivation.go` with:

```go
type DerivedEmitter func(epicID int64, oldStatus, newStatus string) error

func recomputeEpicStatus(db dbQuerier, emit DerivedEmitter, startIDs ...int64) error
```

Algorithm (one pass per `startID`, sharing a single `seen` set across them):

1. Walk ancestors of `startID` **inclusive** via `WITH RECURSIVE ancestors`,
   ordered nearest-first, depth-capped at 32, keeping rows where
   `type_id = TaskTypeEpic`.
2. For each epic in order (nearest ancestor first, so a lower epic's new status
   is visible when a higher epic is recomputed):
   - Skip if already in `seen`; otherwise mark seen.
   - Read current `status`. If it is in the sticky-override set → skip.
   - Read direct children's statuses. Zero children → skip (unchanged).
   - Compute the target status per §1.
   - If `target != current`: execute the SQL `UPDATE tasks SET status = ?,
     completed_at = ?` on the same transaction (completed_at set when target is
     `completed`, else NULL), then call `emit(epicID, current, target)` to record
     the derived event. Propagation to higher epics happens naturally because the
     ancestor walk continues and re-reads the just-updated child status.

`emit` is threaded from `eventcmd.run`: the closure builds the
`epic.status_derived` event (fresh kairos TS, actor `{Kind: ActorSystem,
ID: "epic-derivation"}`), appends it to the current ledger segment, and commits it
(same per-event commit the primary event uses). `emit` is `nil` in the projector
and in unit tests, in which case recompute updates the DB only.

The `seen map[int64]bool` doubles as a cycle guard alongside the depth cap.

## 4. New event kind

`epic.status_derived` joins the closed event-kind vocabulary in `internal/events/event.go`. Payload mirrors `task.status_changed`: `task_id`, `old_status`, `new_status`. The actor on the envelope is `{Kind: system, ID: "epic-derivation"}` with no `session_id`. The projector dispatcher gets a new `replayEpicStatusDerived` that applies the recorded SQL update (status + completed_at) without re-running derivation — the live path already recorded any cascading derived events as their own ledger entries.

Decision rationale: separate event kind, not reusing `task.status_changed`. Filterability is the main benefit (audit log of "what derivation did" stays distinct from human-triggered status changes). Projector complexity is bounded — one new dispatch case.

## 5. Idempotency

- Equality short-circuit: if `target == current`, emit nothing.
- Replay safety: replaying a recorded derivation event re-applies the same SQL update — idempotent at the DB level.
- Single transaction: derivation runs inside the trigger's transaction. A retry of the whole transaction (SQLite busy) recomputes from scratch.
- The `old_status` in the derived event is the epic's status at the moment of recompute, not at the moment of the trigger.

## 6. Cycle detection

Two layers:
- The `WITH RECURSIVE` ancestor walk has a depth cap of 32. `parent_id` cycles are already prevented at write time by `execTaskMoved`'s ancestor-loop check, but the cap is cheap insurance.
- The `seen` set prevents re-visiting an epic within a single derivation pass (and across the two passes `execTaskMoved` makes for the old and new parent chains).

## 7. Tests

New `internal/events/epic_derivation_test.go`:

- Table-driven cases for §1's rule:
  - All needs_plan children → epic needs_plan
  - Mix of needs_plan + ready → epic ready
  - Any in_progress → epic in_progress
  - All terminal → epic completed
  - Mix of ready + terminal → epic ready
  - Mix of needs_plan + terminal → epic needs_plan
  - Zero children → unchanged
- Reopen case: adding a needs_plan child to a completed epic → epic flips back to needs_plan
- Sticky-override states (revisit/declined/obsolete/blocked) on the epic block derivation
- Nested: grand-child status flip propagates up two levels (each level updated)
- Cycle: synthetic parent-loop hits depth cap without panic
- `completed` sets `completed_at`; non-completed clears it
- Idempotency: a second recompute with no change emits nothing and leaves state intact
- Emitter attribution: derived events carry `epicID`, old, and new status; emitter is invoked once per actual change
- End-to-end via `events.Execute` (nil emitter): a child status change flips the parent epic in the DB

## 8. Implementation sequence

1. Add `epic.status_derived` to the closed event-kind vocabulary (`internal/events/event.go`).
2. Create `internal/events/epic_derivation.go` with `DerivedEmitter` + `recomputeEpicStatus`.
3. Wire recompute calls into the executor entry points (§2), threading the emitter through `dispatch`/`Execute`/the `execAndCommit` closures.
4. Construct the emitter in `eventcmd.run` and pass it into the executor entry points.
5. Add `replayEpicStatusDerived` to the projector dispatcher (`internal/events/projector.go`). The projector does NOT call recompute.
6. Tests per §7.
7. Smoke-test against the actual endless DB by creating a synthetic epic + children in the `--db sandbox` and walking through state transitions.

No Python changes. No schema migration (uses existing `tasks.type_id` from E-1538). No new tables.

## 9. Critical files

- `internal/events/event.go` (add event kind)
- `internal/events/executor.go` (wire derivation calls + thread emitter through dispatch)
- `internal/events/projector.go` (replay handler only; no recompute)
- `internal/eventcmd/event.go` (construct + thread the emitter)
- `internal/events/epic_derivation.go` (new)
- `internal/events/epic_derivation_test.go` (new)
- `internal/tasktype/tasktype.go` (uses existing TaskTypeEpic = 4)
