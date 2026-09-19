# E-1442: Document curated 'next' list in `endless guide`

## Motivation

The curated `endless project next` namespace ships several new commands and a new conceptual model (lanes + priority, pending auto-add, event-sourced mutations). Without documentation in `endless guide`, neither human users nor agent sessions discovering endless mid-project will find or understand the feature.

This task extends `endless guide` with the curated-list documentation.

## Scope

Add documentation of the curated next list to the existing `tasks` (or `orchestration`, if it fits better) section of `endless guide`. Decide placement based on existing guide flow — do NOT add a new top-level guide section.

### Topics to cover

1. **Namespace distinction**: `endless project next` (curated, authoritative, per-project) vs `endless task next` (cross-project, algorithmic, untouched by this work).
2. **Lane + priority model**: priority = when to do it (lower = sooner); lane = which code paths conflict; ties at same priority = parallel-safe tracks at that tier; within a lane = sequential. Same-priority lanes render alphabetically.
3. **Pending auto-add**: tasks created or flipped to `phase=urgent` land in `pending`; curator moves them into ranked lanes via mutation commands.
4. **Granular mutation commands**: brief catalog with one-line description of each `lane` and `task` subcommand. Reference E-1438 for the full list.
5. **Batch**: `endless project next batch <ops_file>` runs a file of subcommands under one transaction with a shared `batch_id`.
6. **`--inferred`**: per-project algorithmic view; non-authoritative alternative when the curated list is empty or for sanity-checking.
7. **`briefing`**: situational dashboard for revision decisions.
8. **Event-sourced model in brief**: every mutation emits a row in `project_next_events`; state tables hold the current snapshot; the event log is the audit trail. Reads serve from the state tables with a render-side filter for terminal-status items.

### Style

Agent-friendly few-broad-sections style per existing guide conventions. The reader is an LLM-driven session that needs to know what's available without reading source code. Cross-reference between sections is OK; deep prose is not.

## Decision: `tasks` or `orchestration`?

The curated list is project-scoped and concerns task management, which argues for `tasks`. It also crosses sessions (sessions revise it; sessions read it from briefing), which argues for `orchestration`. Pick based on what reads more naturally given the existing flow of each section.

If unclear when picking up the task: stick with `tasks` (closer to the task subject matter) and add a one-liner under `orchestration` cross-referencing.

## Out of scope

- New top-level `endless guide` section.
- Documentation in `CLAUDE.md` or `README.md`.
- Tutorial-style content (the guide is reference, not tutorial).

## Verification

1. `endless guide tasks` (or `endless guide orchestration` depending on placement) shows the new content alongside existing material.
2. `endless guide --list` does NOT show a new top-level section.
3. Each concept introduced (curated vs `--inferred`, lanes, priority, `pending`, batch, briefing, event-sourced model) is named once in the new content and consistently used thereafter.
4. No regression: existing content in the chosen section is still present and accurate after the additions.
5. An agent that has never seen the curated-list feature can read the guide section and know what commands to run.

## Status

Blocked by E-1441 (the last implementation task in the chain). After it lands, this docs task can faithfully describe what's shipped without speculation.
