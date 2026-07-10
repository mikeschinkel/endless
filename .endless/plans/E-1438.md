# E-1438: Granular curated-next-list mutation commands

## Motivation

The curated `endless project next` list is mutated via append-only event-sourced commands. Each command performs a single conceptual edit (add a lane, move a task, etc.), wraps its row-level work in a SQLite immediate transaction, and emits one row in `project_next_events` capturing what changed. Reads serve from the state tables; the event log is the audit trail.

This task implements the full set of granular mutation commands — the primary write path for the curated list.

## Scope

All mutating commands under `endless project next`. Listed in two groups.

### Lane operations

- `endless project next lane add <json> [--session <id>]` — create one lane from a JSON object: `{"id": "<slug>", "rationale": "<text>", "priority": <int>, "tasks": [...]}`. Tasks array optional.
- `endless project next lane add <lane_id> --rationale <text> [--priority <n>] [--tasks <json>] [--session <id>]` — create from positional + flag args.
- `endless project next lane update <json> [--session <id>]` — bulk update lane fields from a JSON object keyed by `lane_id`.
- `endless project next lane update <lane_id> [--id <new_id>] [--rationale <text>] [--priority <n>] [--tasks <json>] [--session <id>]` — selective update; at least one flag required.
- `endless project next lane reorder <lane_id> --tasks "E-123 E-456 E-789" [--session <id>]` — reorder the tasks within a lane; the listed task ids ARE the new ordering. Must list ALL current tasks in the lane.
- `endless project next lane delete <lane_id> [--session <id>]` — delete the lane. CASCADE removes its tasks via the FK. Emits one `lane.deleted` event (not N).

### Task operations

- `endless project next task add --lane <lane_id> "<json|text>" [--position <n>] [--session <id>]` — add a task to a lane. Default position: end of lane. With `--position N`: insert at index N (1-based); existing tasks at and after shift down by 1. Error if `N > current_lane_len + 1`.
- `endless project next task add --pending "<json|text>" [--session <id>]` — add a task to `project_next_pending`.
- `endless project next task update <task_id> [--reason <text>] [--lane <lane_id>] [--position <n>] [--pending] [--session <id>]` — selective update; at least one flag required.
- `endless project next task move <task_id> --to <lane_id>[:<pos>] [--session <id>]` — sugar for `task update <task_id> --lane <lane_id> --position <pos>`. Cross-lane move: destination's priority is inherited.
- `endless project next task promote <task_id> --to <lane_id>[:<pos>] [--session <id>]` — move task from `project_next_pending` into a lane.
- `endless project next task demote <task_id> [--session <id>]` — move task from its lane back to `project_next_pending`.
- `endless project next task reorder E-123 E-456 E-789 --lane <lane_id> [--session <id>]` — reorder tasks within a lane by listing all current task ids in their new order.
- `endless project next task delete <task_id> [--session <id>]` — delete the task. Emits `task.deleted`.
- `endless project next task delete --lane <lane_id> [--session <id>]` — delete ALL tasks in the lane (lane stays). Emits one `lane.tasks_cleared` event (not N).

## Concurrency

Each command wraps its row-level work in `BEGIN IMMEDIATE TRANSACTION` and emits one row in `project_next_events` as part of the same transaction. On `SQLITE_BUSY` from a concurrent mutation: surface the conflict and exit non-zero.

## `--session <id>` on every command

Optional override for the event's `session_id`. Default: resolver. Use cases: cross-session bookkeeping, replays, sessions specifically created to curate the list.

## Defaults and rules

- **Lane ID regex**: `[a-z_][a-z0-9_]*`. Rejected if it doesn't match.
- **Same-priority lane render order**: alphabetical by `lane_id`. (Affects display; not stored ordering.)
- **`task add` without `--position`**: append to end of lane.
- **`task add` with `--position N` where `N > current_lane_len + 1`**: error before BEGIN.
- **Update commands with no flags**: error ("no instructions").
- **Reorder commands missing tasks**: error if the provided list doesn't include every current task in the lane.
- **Cross-lane `task move`**: destination lane's `priority` is inherited.
- **Operating on a non-existent lane or task**: error before BEGIN.
- **`<text>` shorthand for single-task ops**: `"E-NNNN: <reason>"` parses to `{"id": "E-NNNN", "reason": "<reason>"}`. Quoted per shell rules. JSON required for multi-item payloads.

## Event kinds emitted

| Command | Event `kind` |
|---|---|
| `lane add` | `lane.added` |
| `lane update` | `lane.updated` |
| `lane reorder` | `lane.tasks_reordered` |
| `lane delete` | `lane.deleted` |
| `task add --lane` | `task.added` |
| `task add --pending` | `pending.added` |
| `task update` (reason) | `task.updated` |
| `task update --lane` / `task move` | `task.moved` |
| `task update --pending` / `task demote` | `task.demoted` |
| `task promote` | `task.promoted` |
| `task reorder` | `lane.tasks_reordered` (payload distinguishes from `lane reorder`) |
| `task delete <task_id>` | `task.deleted` |
| `task delete --lane <lane_id>` | `lane.tasks_cleared` |

Payload structure (JSON in the `payload` column): include before-state and after-state for updates; the inserted row for adds; the deleted identifier for deletes. Format consistent across kinds so consumers can replay.

## Schema referenced

```sql
CREATE TABLE project_next_lanes (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  lane_id TEXT NOT NULL,
  priority INTEGER NOT NULL,
  rationale TEXT NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  updated_at TEXT,
  UNIQUE(project_next_id, lane_id),
  FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE project_next_tasks (
  id INTEGER PRIMARY KEY,
  project_next_lane_id INTEGER NOT NULL,
  task_id TEXT NOT NULL,
  reason TEXT NOT NULL,
  position INTEGER NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  updated_at TEXT,
  UNIQUE(project_next_lane_id, task_id),
  UNIQUE(project_next_lane_id, position),
  FOREIGN KEY (project_next_lane_id) REFERENCES project_next_lanes(id) ON DELETE CASCADE
);
```

Plus `project_next`, `project_next_pending`, `project_next_events` per E-1434.

## Out of scope

- Schema (E-1434).
- Read commands (E-1440 `show`, E-1439 `briefing`).
- Hook subscribers for auto-add (E-1437).
- Batch wrapper (E-1436, depends on this task).
- `--inferred` algorithmic view (E-1441).

## Design notes

- **Shared helper**: each handler does the same outer pattern — resolve session_id, BEGIN IMMEDIATE, perform row work, INSERT INTO project_next_events, COMMIT. Factor out the begin-emit-commit wrapper so individual handlers just produce a "what to mutate" intent and the wrapper does the rest.
- **Reorder semantics**: for both `lane reorder` and `task reorder` with a list of all current items, the implementation is "DELETE all positions in lane; INSERT in the listed order" within the transaction. Single semantic event even though many rows churn.
- **Cross-lane move on a task**: detect when `--lane` is supplied and differs from current. Update the FK and recompute positions in both source and destination lanes. Single `task.moved` event.
- **Lane-id rename via `lane update --id <new_id>`**: validate new id against the regex; reject collision with another lane in the same project. Single `lane.updated` event.
- **`updated_at` columns** on `project_next_lanes` and `project_next_tasks`: set on update; leave NULL until first update. Read commands surface `added_at` and (if present) `updated_at`.

## Verification

For each command, at least one happy-path test and one rejection-path test:

- `lane add` (args, then JSON): happy; reject duplicate `lane_id`; reject `lane_id` not matching regex.
- `lane update`: rename via `--id`; change `--priority`/`--rationale`; reject no-flags.
- `lane reorder`: full list with all tasks; reject if any current task missing from the list; reject if a listed task isn't in the lane.
- `lane delete`: cascades to tasks; one event (not N).
- `task add --lane`: append + at specific position; reject lane not found; reject `--position > len+1`; reject text-form malformed.
- `task add --pending`: appears in pending; idempotent on conflict.
- `task update`: reason change; lane change (cross-lane move); pending demote; reject no-flags.
- `task move` / `promote` / `demote`: behavior matches the equivalent `task update`.
- `task reorder` with `--lane`: same correctness as `lane reorder`.
- `task delete <id>`: by id; emits event.
- `task delete --lane <id>`: clears tasks; lane stays; emits single `lane.tasks_cleared` event.
- BEGIN IMMEDIATE conflict: two concurrent mutations → one wins, other gets a clear `SQLITE_BUSY` error.
- `--session <id>` override: event's `session_id` matches the override, not the resolver default.

## Status

Ready to claim. Schema (E-1434) is shipped. Downstream tasks E-1436 (batch) and E-1440 (read) depend on this.
