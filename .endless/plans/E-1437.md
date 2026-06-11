# E-1437: Urgent auto-add hook subscribers for curated next list

## Motivation

When a task is created or flipped to `phase=urgent`, the curator hasn't yet decided which lane it belongs to — but the urgency signal should not be lost. Auto-adding the task to a `pending` bucket gives the curator a visible queue of "things flagged urgent that I haven't categorized yet," surfaced on every `endless project next show` and in the briefing dashboard.

This task wires two hook subscribers that maintain the `project_next_pending` table without operator intervention.

## Scope

Subscribe to two events in the task-mutation code path (the single function in endless that handles task creates and phase changes — single point of truth, not external hooks):

1. **`task.created` with `phase=urgent`** → INSERT into `project_next_pending` with `reason='auto-added: phase=urgent'` and `ON CONFLICT(project_next_id, task_id) DO NOTHING`. Then emit one event in `project_next_events` with `kind='pending.added'` and a payload identifying the task.

2. **`task.phase_changed` to `urgent`** → same INSERT + event pattern. (Either trigger: same `reason` text.)

The `project_next_id` is resolved by looking up the row in `project_next` for the task's project. If no row exists for the project, the subscriber creates one (no `project_next_pending` insert without a `project_next` parent — that violates the FK).

The INSERT is single-row. **No `BEGIN IMMEDIATE TRANSACTION` is required** — the on-conflict idempotency is enforced by the UNIQUE constraint, not a multi-row coordination concern. The event INSERT and the pending INSERT do go in one small transaction for atomic visibility, but a SQLite default deferred transaction suffices.

## Schema referenced

```sql
CREATE TABLE project_next_pending (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  task_id TEXT NOT NULL,
  reason TEXT NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  UNIQUE(project_next_id, task_id),
  FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE project_next_events (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  session_id INTEGER NOT NULL,
  event_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  kind TEXT NOT NULL,
  payload TEXT,
  batch_id INTEGER,
  FOREIGN KEY (project_next_id) REFERENCES project_next(id),
  FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

## Out of scope

- Schema (E-1434 — done; tables exist as shown above).
- `task.status_changed` to terminal → no subscriber needed; render-side filter handles visibility.
- Auto-add triggers for phases other than `urgent` (deferred until the missing signal becomes visible).

## Design notes

- **Idempotency on the INSERT.** UNIQUE constraint + `ON CONFLICT DO NOTHING` ensures repeated triggers (urgent → next → urgent again) don't duplicate the pending row.
- **Event behavior on conflict.** Choose ONE policy and document it in the verification block:
  - Option A: also suppress the event when the INSERT was a no-op (preferable — "we attempted to add but it was already there" isn't a state change).
  - Option B: emit the event regardless (each trigger captured).
  The implementer picks; this is a pickup-time decision because both work and the choice depends on inspection of how other endless event consumers expect duplicates.
- **Session attribution.** Use the resolver to get the session id at trigger time. Since this fires from the task-mutation code path inside the user-facing session's process, the resolver should return that session naturally.
- **Wrap the pending INSERT and the event INSERT in one transaction** so a reader of `project_next_events` is guaranteed to see the `project_next_pending` row that the event references.

## Verification

1. `endless task add "<title>" --phase urgent --project endless` → exactly one new row in `project_next_pending` with `reason='auto-added: phase=urgent'`. One `pending.added` event in `project_next_events`.
2. `endless task update X --phase urgent` where X was previously `now`/`next`/etc. → same: one row, one event.
3. Same task: `phase=urgent` → `phase=next` → `phase=urgent` again → exactly one row in `project_next_pending` (not three; UNIQUE enforces). Event behavior on duplicate-attempt matches the policy chosen and documented.
4. Non-urgent phase change (e.g. `next` → `now`) → no insert into `project_next_pending`, no `pending.added` event.
5. Auto-add fires even when no curated list exists for the project yet (subscriber creates the `project_next` row before inserting pending).

## Status

Ready to claim. Depends on E-1434 only (schema), which is shipped.
