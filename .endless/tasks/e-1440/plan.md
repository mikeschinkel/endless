# E-1440: `endless project next show` curated-list read command

## Motivation

Reading the curated list is the most-common operation against `endless project next`. The default invocation (bare `endless project next`) and the explicit `show` subcommand both render the list; `--llm` and `--json` modes serve agents and machine consumers.

This task ships the read command. Its formatting design is intentionally deferred to implementation time so it can be designed against real data from the write commands.

## Deferred formatting design

The output formatting is deferred:

> "Formatting design happens at implementation time using real-shape data from sub-tasks 2-5."

Sub-tasks 2-5 are E-1436 (batch), E-1437 (hooks), E-1438 (granular mutations), E-1439 (briefing). By the time E-1440 is picked up, those should have landed and the list can be populated with real curated lanes + tasks + pending entries.

**Before writing the read implementation, the picking-up session writes a plan file at `.endless/plans/E-1440.md` proposing the formatting**, covering:

- Default text format (terminal-width-respecting; lane grouping visible; priority ordering visible; same-priority lanes grouped together; alphabetical within same priority)
- `--llm` format (terse for agents; key-value-ish; no decoration)
- `--json` format (must match the JSON output shape below)
- Empty-list output: one-line message + hint pointing at `--inferred`
- Whether drift indicators (a list item whose task status changed since the last event) surface in this command or stay in `briefing` only

The implementing session attaches the plan via `endless task update E-1440 --text .endless/plans/E-1440.md`, presents to Mike, and gets sign-off before writing the read code.

## Scope (after formatting plan is approved)

Read commands:

- `endless project next [--project <p>] [--json] [--llm]` — bare form; same content as `show`.
- `endless project next show [--project <p>] [--json] [--llm]` — explicit form.

Implementation:

- **Joins**: `project_next` → `project_next_lanes` → `project_next_tasks` and `project_next_pending`, plus a join to `tasks` for the render-side terminal-status filter.
- **Render-side filter**: hide list items whose underlying task status is `confirmed`, `assumed`, `declined`, or `obsolete`. The items remain in `project_next_tasks` (no compaction); the read just doesn't render them.
- **Lane grouping**: sort by `priority` ascending. Same-priority lanes render in alphabetical `lane_id` order. Within a lane, tasks render by `position` ascending.
- **`pending` section**: rendered after the ranked lanes.
- **Revised header**: `revised` timestamp (from `MAX(event_at)` in `project_next_events`) and `session: {id, uuid}` for the responsible session of that latest event.

## JSON output shape

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
      "id": "<lane_id>",
      "priority": 1,
      "rationale": "<longer text>",
      "tasks": [
        {
          "id": "E-NNNN",
          "reason": "<60-80 char>",
          "added": "2026-05-18T14:30:00Z",
          "updated": "2026-05-19T08:00:00Z"
        }
      ]
    }
  ],
  "pending": [
    {
      "id": "E-NNNN",
      "reason": "auto-added: phase=urgent",
      "added": "2026-05-17T10:00:00Z"
    }
  ]
}
```

`updated` field omitted when equal to `added` (i.e., the task hasn't been updated since add). Order of `lanes` and `pending` arrays is intrinsic per the render rules (priority asc, then alphabetical lane_id within ties; pending by `added_at` asc).

## Out of scope

- Mutations (E-1438), batch (E-1436), briefing (E-1439), `--inferred` (E-1441).
- Storage compaction (deferred YAGNI per E-1421).

## Schema referenced

State tables (`project_next`, `project_next_lanes`, `project_next_tasks`, `project_next_pending`), the event log (`project_next_events`), and `tasks` for the render-side filter join.

## Design notes

- **Render-side filter is the only "drop" mechanism.** Storage stays. The same items remain in `project_next_tasks` for any consumer that wants the historical or full view.
- **The default `endless project next` IS `show`** — they share the same code path with no separate fork. Argument parsing dispatches to the same handler.
- **The `--inferred` flag belongs to E-1441**, not this command. `endless project next --inferred` is a different code path entirely (algorithmic ranking), wired by E-1441 to coexist with the curated default.

## Verification (after formatting plan is approved and implemented)

1. Populated list → output matches the approved formatting plan in default mode.
2. Empty list (no lanes, no pending) → empty-state message + `--inferred` hint per the approved plan.
3. List contains a terminal-status task (e.g. `confirmed`) → render-side filter hides that item; raw `project_next_tasks` still contains the row.
4. Same-priority lanes → render in alphabetical `lane_id` order.
5. `--llm` → terse per the plan; no decoration; agent-parseable.
6. `--json` → structured per the shape above, including `session: {id, uuid}` nested form, `tasks` array (not `items`), and `pending` (not `pending_triage`).

## Status

Blocked by E-1438 (write side) and E-1439 (briefing — formatting reuse and design precedent). After both land, the formatting plan can be authored against real data.
