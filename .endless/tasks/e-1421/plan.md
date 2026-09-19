# Curated per-project "next" list with lanes and consistency hooks

## Motivation

Across many parallel sessions, the working "what's next for this project" decision keeps getting re-derived from scratch — each fresh session re-reads tasks, re-ranks, re-explains. Mike asks "what's next" repeatedly because the answer isn't persisted. The current `endless task next` (which is heuristic and cross-project) cannot capture the parallelization analysis a curating session produces.

This task adds a **curated, persistent, per-project "next" list** as a first-class artifact at a new namespace: `endless project next`. Sessions mutate it via granular append-only commands; Mike reads it; consistency updates (auto-add on urgent, render-side terminal-status filter) keep it honest without manual maintenance. The existing `endless task next` is untouched.

## Confirmed design decisions

- **New namespace**: `endless project next`. The curated list is a project-level artifact, not a task-level one. `endless task next` (cross-project, algorithmic) keeps its existing shape. A future `endless session next` is decoupled from this work.
- **Authoritative, not heuristic.** Empty list = empty list. The algorithmic per-project view is exposed via `endless project next --inferred`.
- **Per-project.** Cross-project ranking is out of scope.
- **Soft cap 5-10 items**, hard cap 25. Warning on commands that exceed soft cap; refusal at hard cap.
- **Two-axis model: priority + lane.** Each lane carries an integer `priority` (lower = sooner) AND a free-text `id`. Multiple lanes may share a priority — that means "parallel-safe at this tier." Within a lane: tasks are sequential. Across lanes at the same priority: tasks are parallel-safe.
- **Lane id format**: `[a-z_][a-z0-9_]*` (snake_case identifier). Rejected if it doesn't match.
- **Same-priority lane render order**: alphabetical by `id` (deterministic; no implicit ordering by creation time or position).
- **Pending-triage bucket**: auto-added urgent tasks land here unranked. Curation moves them into ranked lanes.
- **Render-side filter for terminal status.** Render joins against `tasks` and hides items with terminal status. Race-free by construction. Storage compaction is deferred (YAGNI).
- **Event-sourced mutations.** Every command emits one event in `project_next_events`. State tables (`project_next_lanes`, `project_next_tasks`, `project_next_pending`) hold current snapshot, updated atomically with the event under `BEGIN IMMEDIATE TRANSACTION`. Audit/replay queries scan the events table.
- **One event per command** (never amend). If multiple ops should land as one logical change, use `batch`.
- **Concurrency via `BEGIN IMMEDIATE TRANSACTION`.** Each mutating command wraps its work in a transaction; the loser of a concurrent attempt gets `SQLITE_BUSY` and surfaces the conflict.
- **`--session <id>` optional override** on mutating commands. Default: resolver auto-detect. Use case: cross-session bookkeeping, sessions specifically created to manage the list, replays.
- **JSON, array-of-structs (no maps).** Order intrinsic in arrays.
- **Per-task `reason` (~60-80 chars)**, per-lane `rationale` (longer). Both required.
- **`<text>` shorthand** for single-task ops: `"E-NNNN: <reason>"` parses to `{id, reason}`. Quoted per shell rules. JSON required for multi-item payloads.
- **Output convention** follows existing endless commands: default = human-readable text; `--llm` = terse agent-friendly text; `--json` = structured JSON.

## SQL schema

Modeled storage with event log. Per house rules: every table starts with `id INTEGER PRIMARY KEY`; FK columns use `_id` suffix; multi-segment names avoided by table-name disambiguation.

```sql
-- One row per project (header + FK target)
CREATE TABLE project_next (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL UNIQUE,
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Lanes within a project's curated list
CREATE TABLE project_next_lanes (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  lane_id TEXT NOT NULL,                  -- matches [a-z_][a-z0-9_]*
  priority INTEGER NOT NULL,
  rationale TEXT NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  updated_at TEXT,                        -- nullable; set only when updated post-add
  UNIQUE(project_next_id, lane_id),
  FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

-- Tasks within a lane, ordered by position
CREATE TABLE project_next_tasks (
  id INTEGER PRIMARY KEY,
  project_next_lane_id INTEGER NOT NULL,
  task_id TEXT NOT NULL,
  reason TEXT NOT NULL,
  position INTEGER NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  updated_at TEXT,                        -- nullable; set only when updated post-add
  UNIQUE(project_next_lane_id, task_id),
  UNIQUE(project_next_lane_id, position),
  FOREIGN KEY (project_next_lane_id) REFERENCES project_next_lanes(id) ON DELETE CASCADE
);

-- Auto-added urgent tasks awaiting curation
CREATE TABLE project_next_pending (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  task_id TEXT NOT NULL,
  reason TEXT NOT NULL,
  added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  UNIQUE(project_next_id, task_id),
  FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

-- Event log (append-only audit of every mutation)
CREATE TABLE project_next_events (
  id INTEGER PRIMARY KEY,
  project_next_id INTEGER NOT NULL,
  session_id INTEGER NOT NULL,
  event_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
  kind TEXT NOT NULL,                     -- 'lane.added', 'lane.updated', 'lane.deleted',
                                          -- 'task.added', 'task.updated', 'task.moved',
                                          -- 'task.promoted', 'task.demoted', 'task.deleted',
                                          -- 'lane.reordered', 'pending.added', etc.
  payload TEXT,                           -- JSON payload for the event
  batch_id INTEGER,                       -- nullable; set when emitted as part of a batch
  FOREIGN KEY (project_next_id) REFERENCES project_next(id),
  FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

Convention notes (per 349ecc8):
- `project_next.project_id INTEGER` (FK to `projects(id)`), not `project TEXT`. Matches the `_id` suffix rule and the FK pattern every other table uses.
- Timestamps are `TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))`. SQLite has no real TIMESTAMP type; every prior migration uses TEXT with ISO-8601 strftime defaults.
- FK constraints declared as separate `FOREIGN KEY ... REFERENCES ...` clauses, not inline shorthand.

Notes on the schema change from E-1434's shipped form:
- `project_next_items` → `project_next_tasks` (matches JSON `tasks` array name).
- `project_next_revisions` → `project_next_events` (matches event-sourced framing).
- Added `added_at` + nullable `updated_at` on `project_next_lanes` and `project_next_tasks` (used by the read command's output).
- Added `batch_id` on events (nullable).
- The schema corrections from 349ecc8 (project_id FK, TEXT timestamps) are already in main; this rewrite carries them forward.

E-1434 shipped the original schema as migrateV11. A follow-up sub-task migrates to this updated shape (drop-and-recreate is acceptable per no-migration-for-unshipped-software).

## JSON output shape

Output of `show --json`. Input shapes for mutating commands are smaller per-operation payloads.

```json
{
  "project": "endless",
  "revised": "2026-05-18T14:30:00Z",
  "session": {
    "id": 468,
    "uuid": "abcdef..."
  },
  "lanes": [
    {
      "id": "<slug>",
      "priority": 1,
      "rationale": "<longer text>",
      "tasks": [
        {
          "id": "E-NNNN",
          "reason": "<60-80 char>",
          "added": "...",
          "updated": "..."
        }
      ]
    }
  ],
  "pending": [
    {
      "id": "E-NNNN",
      "reason": "auto-added: phase=urgent",
      "added": "..."
    }
  ]
}
```

`updated` field omitted when equal to `added`. `revised`/`session` describe the most recent event in `project_next_events`.

## CLI surface

Namespace: `endless project next ...`. Hierarchical subcommand structure.

### Read

```
endless project next [--project <project>]                                   # default = show
endless project next show [--project <project>] [--json] [--llm]             # explicit; same content
endless project next --inferred [--project <project>]                         # per-project algorithmic view (new feature)
endless project next briefing [--json] [--llm]                                # situational dashboard for revision decisions
```

### Lane operations

```
endless project next lane add <json> [--session <id>]
endless project next lane add <lane_id> --rationale <text> [--priority <n>] [--tasks <json>] [--session <id>]
endless project next lane update <json> [--session <id>]
endless project next lane update <lane_id> [--id <new_id>] [--rationale <text>] [--priority <n>] [--tasks <json>] [--session <id>]
endless project next lane reorder <lane_id> --tasks "E-123 E-456 E-789" [--session <id>]
endless project next lane delete <lane_id> [--session <id>]
```

### Task operations

```
endless project next task add --lane <lane_id> "<json|text>" [--position <n>] [--session <id>]
endless project next task add --pending "<json|text>" [--session <id>]
endless project next task update <task_id> [--reason <text>] [--lane <lane_id>] [--position <n>] [--pending] [--session <id>]
endless project next task move <task_id> --to <lane_id>[:<pos>] [--session <id>]      # sugar for task update --lane <X> --position <pos>
endless project next task promote <task_id> --to <lane_id>[:<pos>] [--session <id>]   # pending → lane sugar
endless project next task demote <task_id> [--session <id>]                            # lane → pending sugar
endless project next task reorder E-123 E-456 E-789 --lane <lane_id> [--session <id>]  # explicit-list reorder
endless project next task delete <task_id> [--session <id>]
endless project next task delete --lane <lane_id> [--session <id>]                     # clear all tasks in lane
```

### Batch

```
endless project next batch <ops_file> [--session <id>]
```

`<ops_file>` format: one subcommand per line, minus the `endless project next ` prefix. Comments prefixed with `#`. Each line follows shell-style argument parsing (quoting required for args with spaces or shell metacharacters). Example:

```
# Seed the curated list for endless
lane add resolver --rationale "Stop silent session-id misattribution" --priority 1
task add --lane resolver "E-1415: DB existence check; loud failure when companion stale"
task add --lane resolver "E-1416: Pre-rebase dirty-tree check; safety net"

lane add observability --rationale "Visibility into worktree/session lifecycle" --priority 2
task add --lane observability "E-1391: Worktree lifecycle events + session_worktrees table"
```

Plumbing: parser reads lines, dispatches each as the normal subcommand handler inside one `BEGIN IMMEDIATE TRANSACTION`. Each line emits its own event; all events share a generated `batch_id`. Fail-atomic: if any line errors, the whole batch rolls back.

E-1527 generalizes this mechanism CLI-wide; for E-1421's scope it's specific to `endless project next batch`.

## Defaults and rules

- **Task add without `--position`**: append to end of lane.
- **Task add with `--position 1`**: insert at front; existing tasks shift down.
- **Task add with `--position N` where N > len+1**: error.
- **Lane add without `--priority`**: append at lowest priority + 1 (or `1` if empty).
- **Update commands with no flags**: error ("no instructions").
- **Reorder commands missing tasks**: error (must list all current tasks in the lane).
- **Lane ID regex**: `[a-z_][a-z0-9_]*`. Names that don't match are rejected.
- **Same-priority lane render order**: alphabetical by `lane_id`.
- **Cross-lane move**: destination lane's priority is inherited (the task just moves; priority is per-lane, not per-task).
- **Add to non-existent lane**: error.
- **Operating on non-existent task**: error.
- **`--session <id>`**: optional override on mutating commands; default = resolver auto-detect.
- **`<text>` form**: `"E-NNNN: <reason>"` parses to `{id, reason}`. Quoted per shell rules.

## Hook integration

Two event subscribers maintain consistency:

1. **`task.created` with `phase=urgent`** → `INSERT INTO project_next_pending(project_next_id, task_id, reason, added_at) VALUES (?, ?, 'auto-added: phase=urgent', NOW()) ON CONFLICT(project_next_id, task_id) DO NOTHING`. Emit a `pending.added` event with `kind='pending.added'` and a payload identifying the auto-add origin.
2. **`task.phase_changed` to `urgent`** → same.

Triggered from the task-mutation code path (single point of truth). Idempotent via the UNIQUE constraint. No `BEGIN IMMEDIATE TRANSACTION` needed for single-row INSERT.

Terminal-status changes need no subscriber — render-side filter handles visibility.

## Resolved

1. **Storage**: modeled (5 tables: `project_next`, `project_next_lanes`, `project_next_tasks`, `project_next_pending`, `project_next_events`). Concurrency via `BEGIN IMMEDIATE TRANSACTION`.
2. **Lane id format**: free-text slug matching `[a-z_][a-z0-9_]*`.
3. **`task move` syntax**: `--to <lane_id>[:<position>]`. Cross-lane supported. Sugar for `task update --lane X --position N`.
4. **`pending` lifecycle after curation**: items stay until explicitly moved, or until their task hits terminal status (render-side filter hides them).
5. **Compaction of storage**: deferred (YAGNI).
6. **Same-priority lane order**: alphabetical by `lane_id`.
7. **One event per command**: never amend. Batch for multi-command-as-one-logical-change.
8. **Namespace**: `endless project next`. `endless task next` untouched.
9. **`--session <id>`**: optional override; default = resolver auto-detect.
10. **`<text>` shorthand**: `"E-NNNN: <reason>"`. Quoted per shell rules.
11. **Hierarchical vs compound subcommands**: hierarchical only (compound aliases dropped).

## Sub-tasks status

| ID | What | Status | Notes |
|---|---|---|---|
| E-1434 | Schema + migration | `assumed` | Shipped original schema (`project_next_items`, `project_next_revisions`). Needs a follow-up to rename to `project_next_tasks`, `project_next_events`, add `added_at`/`updated_at`/`batch_id` columns. File as new ticket. |
| E-1436 | (was "revise"; revisit) | `revisit` | Repurpose as: implement `endless project next batch <ops_file>`. The BEGIN IMMEDIATE + validation + executor scaffolding already written serves this directly. |
| E-1437 | Urgent auto-add hooks | `ready` | Reuse with table-name update; minor edit. |
| E-1438 | Granular mutation commands | `ready` | Primary write path. Implements all the lane and task subcommands. |
| E-1439 | Briefing | `ready` | Namespace update; otherwise as specified. |
| E-1440 | Read (`show`) | `ready` | Namespace update. Formatting plan still deferred to file-time per existing prompt. |
| E-1441 | `--inferred` view | `ready` | Reframe: implement `endless project next --inferred` as a per-project algorithmic view (new feature, NOT a migration of `endless task next`, which stays untouched). |
| E-1442 | Docs | `ready` | Extend existing `tasks` section (or possibly `orchestration`, given the cross-session implications) with curated-list documentation. |

Prompts for E-1436 through E-1442 need regeneration after this plan update.

## Deferred (don't implement yet)

- Daemon + `claude -p` for periodic auto-revision. Cost and churn risk.
- Auto-add triggers beyond `urgent`. Add when the missing signal becomes visible.
- Cross-project ranking. Curated stays single-project.
- Storage compaction for terminal-status items. Render-side filter handles correctness.
- Generalized CLI-wide `batch` mechanism — tracked as E-1527.
- `endless session next` — separate future feature, decoupled.

## Status

Plan revised 2026-06-01 (granular-mutations architecture, new namespace, expanded sub-task scope). Ready for sub-task prompts to be regenerated and E-1436 to be reframed.
