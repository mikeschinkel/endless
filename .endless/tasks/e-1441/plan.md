# E-1441: `endless project next --inferred` per-project algorithmic view

## Motivation

The default `endless project next` reads the curated list (authoritative, manually maintained). When no curated list exists or when a session wants a heuristic ranking of all current actionable tasks in a project, `--inferred` provides an algorithmic ranking — non-authoritative, useful as a starting point or sanity check.

This is a **new** feature. It is NOT a migration of the existing `endless task next` (which is cross-project, algorithmic, and untouched). `--inferred` may share ranking-signal infrastructure with `endless task next`, but its scope, entry point, and behavior are new.

## Scope

Implement `endless project next --inferred`:

- **Per-project scope**: applies algorithmic ranking signals to a single project's tasks. Default project: resolved from cwd. Override: `--project <project>`.
- **Ranking signals** (at minimum, mirroring `endless task next`'s heuristic scoped to one project):
  - Phase ordering: `urgent` > `now` > `next` > `later` > `maybe`.
  - Status filter: only show actionable statuses (`ready`, `needs_plan`, `in_progress`); exclude terminal (`confirmed`, `assumed`, `declined`, `obsolete`) and `revisit`/`blocked` per the existing algorithm's behavior.
  - Recency: more-recently-updated tasks rank higher within the same phase.
  - Blocked-by-resolved: tasks whose blockers are all in terminal status rank higher than transitively-blocked tasks.
  - Dependency depth: parents that are blocked by their own children sit lower than leaf tasks.
- **Output modes**: default text, `--llm` terse, `--json` structured.
- **Coexistence with curated default**: `endless project next` (no flag) reads the curated list (E-1440). `endless project next --inferred` runs the algorithmic ranking. Both work; they're independent code paths under the same prefix.

## NOT a migration

`endless task next` is NOT being moved or changed. It stays a cross-project algorithmic command at its existing entry point with its existing behavior. `--inferred` is a NEW per-project view in the `endless project next` namespace.

The ranking infrastructure inside `endless task next` is allowed to be shared with `--inferred` (factor out a common helper if that's cleaner), but no external callers of `endless task next` should change.

## Out of scope

- The curated read command itself (E-1440 — must land first since `--inferred` coexists with the default curated path).
- Mutations (E-1438), batch (E-1436), briefing (E-1439).
- Any changes to `endless task next` (untouched).

## Schema referenced

`tasks` and any related tables used by `endless task next` for ranking signals (`task_deps`, `session_tasks`, etc.). No tables specific to the curated list are required for `--inferred`.

## Design notes

- **Ranking algorithm**: at minimum, mirror `endless task next`'s heuristic, scope-restricted to a single project's task set. Refinements (per-project recency weights, etc.) can be follow-up tickets.
- **`endless project next show` and `endless project next --inferred` are different commands at the same prefix.** Argument-parser dispatch: if `--inferred` is set, route to the algorithmic ranking; else, route to the curated read (E-1440).
- **No write side** — purely read; no transaction; no `--session <id>` override.

## Verification

1. `endless project next --inferred --project endless` → per-project algorithmic ranking. Returns actionable tasks in this project, ranked.
2. `endless project next --inferred` (no `--project`) → uses current project from cwd.
3. `endless project next` (no `--inferred`) → still reads the curated list. The two coexist.
4. `endless task next` behavior unchanged. Existing callers see no change.
5. `--llm` and `--json` modes produce the expected formats.
6. Terminal-status tasks (`confirmed`, `assumed`, `declined`, `obsolete`) are excluded.

## Status

Blocked by E-1440 (the curated read must exist to coexist with `--inferred` at the same prefix).
