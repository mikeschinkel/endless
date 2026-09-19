# E-1767 — Record a session_tasks touch on link/block by porting the task-relation write to the Go event model

## Context

session_tasks records "the session interacted with this task," not merely "worked
on it." Today `link`/`block`/`unblock`/`unlink` write the `task_deps` table via a
direct Python SQLite `db.execute` in task_cmd.py (`link_tasks` / `unlink_tasks`)
and emit NO event — a pre-event-model E-957 path. Session touches are recorded
only as a side-effect INSIDE the Go event executors (`upsertSessionTask` in
internal/events/executor.go), so a write that never reaches an executor can never
record a touch. Result: linking or blocking a task never enrolls it in
session_tasks. Recording the touch therefore requires routing the task-relation
write through the Go event executor model.

## Approach

Port the task-relation write path from the direct Python SQLite write to the Go
event + executor model, and record the touch in the new executor. This both fixes
the missing touch AND removes a legacy Python SQLite write, advancing E-1486.

## Coordination (relations, not hard blocks)

- E-1486 (epic: remove Python SQLite access by porting to Go) — this task is an
  instance of that port for the task-relation write path. relates_to E-1486.
- E-1389 (rename task_deps -> task_relations; target_type -> target_kind; drop
  source_type; metadata-only) restructures the SAME table. If E-1389 lands first,
  target the new names; if not, target task_deps and let E-1389's migration update
  references. Sequencing preference, not a gate. relates_to E-1389.
- Parent E-1462 (the session_tasks relation-capture foundation this extends);
  complements E-1696 (the broader referenced/queued capture).

## Implementation

1. Emit events from Python instead of writing the table directly. In task_cmd.py,
   `link_tasks` emits `task_dep.created` and `unlink_tasks` emits
   `task_dep.deleted` via `event_bridge.emit_event`, mirroring the existing
   `task.status_changed` emit pattern already in task_cmd.py (payload carries
   source_id, target_id, dep_type). Remove the direct `db.execute` INSERT/DELETE
   into task_deps — the Go executor now owns the write. The `block`/`unblock`
   verbs already delegate to link/unlink, so they inherit the emit path.
2. Add the Go executors in internal/events/executor.go:
   `execTaskDepCreated` / `execTaskDepDeleted` that (a) apply the row
   insert/delete against task_deps (or task_relations, per E-1389), and (b) when
   `shouldRecordSessionTouch(evt)`, call
   `upsertSessionTask(db, evt.Actor.SessionID, id, RelationRevisited)` for BOTH
   endpoints — the session interacted with the subject and the referenced task.
   Register the two kinds in the executor dispatch switch, add matching projector
   replay handlers in projector.go so ledger replay reproduces the rows, and add
   payload structs in payload.go plus whatever event.go registration the other
   `task.*` kinds carry.
3. Confirm the existing `KindTaskDepCreated` / `KindTaskDepDeleted` constants
   (already defined in event.go) are the ones emitted, and remove any now-dead
   direct-write code paths.

## Verification — tests/tasks/e-1767-verify.sh

Isolated env, model on an existing tests/tasks/e-NNNN-verify.sh (single command).
Assert, with the CLI run from a session context so the actor carries a SessionID:
- After `task link A --to B`, session_tasks has a `revisited` row for the acting
  session covering the involved tasks (that were not already enrolled).
- After `task block A --by C`, the same.
- Projector replay of the ledger reproduces the task_deps (or task_relations)
  rows (no drift between live executor and replay).
Plus Go executor unit tests for `execTaskDepCreated` / `execTaskDepDeleted`:
touch recorded for both endpoints, and skipped when the actor has no SessionID.

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1767-verify.sh`.
