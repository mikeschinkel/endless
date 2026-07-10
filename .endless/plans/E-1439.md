# E-1439: `endless project next briefing` situational read

## Motivation

When a session needs to revise the curated list — adding lanes, moving tasks, deciding what's truly next — it needs context beyond just the current list contents. What changed recently? Which tasks just became unblocked? Are list items now in a status where they should be dropped? What's in `pending` waiting for curation? What state are the worktrees in?

`briefing` composes existing queries into one dashboard. It is read-only and has no concurrency requirements.

## Scope

Implement `endless project next briefing` — a single command that prints (or returns as JSON) a dashboard of fields useful for revision decisions.

### Fields

1. **Current curated list** — same content as `endless project next show`, with the `revised` timestamp (from `MAX(event_at)` in `project_next_events` for this project's `project_next_id`) prominently displayed, plus the responsible `session_id` from that latest event.
2. **Drift indicators** — list items whose underlying task status has changed since the last event in `project_next_events`. "Drift" is the set difference: items whose `tasks.updated_at > MAX(project_next_events.event_at)` for the project. Highlight which ones.
3. **Recently changed tasks (last 24h)** — `SELECT id, title, status, phase, updated_at FROM tasks WHERE project = ? AND updated_at > datetime('now', '-1 day') ORDER BY updated_at DESC`.
4. **In-progress tasks across all sessions** — join `tasks` with `session_tasks` (or wherever the claim is recorded) to show which task is claimed by which session, filtered to this project.
5. **Tasks in `verify` status** — candidates to confirm-and-drop from the list.
6. **Blockers that just resolved** — tasks where a `blocked_by` dep is now in a terminal status (`assumed`, `confirmed`, `declined`, `obsolete`), so the dependent is newly actionable.
7. **Worktree states** — for each `.endless/worktrees/e-<NNN>` directory: dirty tree? (`git status --porcelain` non-empty) and branch behind main? (`git rev-list --count main..HEAD` > 0).

### Output modes

- **Default** — verbose human-readable text (dashboard layout with section headers).
- **`--llm`** — terse agent-friendly text (no decoration; key-value or short table per section).
- **`--json`** — structured JSON with one field per section.

### Iteration expected

This is a first cut. The exact layout, the field set, and the format of each field will need refinement after real usage. Ship what's specified; file follow-up tickets for refinements as they surface.

## Out of scope

- The `show` command (E-1440).
- Mutations (E-1438).
- Batch (E-1436).

## Design notes

- **No write side**: read-only command. No transaction. No `--session <id>` override.
- **Per-project scope**: the `--project <project>` flag selects which project's curated list to operate on. Default: resolved from cwd.
- **Reuse existing query infrastructure**: `task active`, `task next` (the algorithmic one), `worktree list`, etc. all do versions of these queries. Compose rather than reinvent.
- **Drift calculation**: a simple `WHERE tasks.updated_at > (SELECT MAX(event_at) FROM project_next_events WHERE project_next_id = ?)` against the list's task ids.

## Schema referenced

`project_next`, `project_next_lanes`, `project_next_tasks`, `project_next_pending`, `project_next_events`, plus `tasks`, `session_tasks`, `sessions`. No new schema; only joins.

## Verification

1. Populated curated list → all 7 fields render with content; default text has clear section headers.
2. No curated list yet for the project → tells you so in the "Current list" section; other 6 fields still render against project state.
3. Recently-updated task that's on the list → appears in both "Current list" (as a drift indicator) AND in "Recently changed tasks" (last 24h).
4. Blocker resolved (a task's blocker is now `assumed`) → appears in "Blockers that just resolved."
5. Dirty worktree → flagged in "Worktree states."
6. `--llm` → terse, no decoration; agent-parseable.
7. `--json` → structured matching field names; values for each section parseable into native types where applicable (timestamps, integers, strings).

## Status

Ready to claim. Depends on E-1434 only (schema for the joins).
