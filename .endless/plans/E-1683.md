# Session-scoped task order + Claude-set sequencing

## Why
Mike repeatedly asks a session "what order should we implement these?" Capture that answer as data so `session next` shows the do/plan group in implementation order, with parallel groups visible — empowering him to run more in-flight tasks without losing track.

## Data
- Add `session_tasks.order` INTEGER (nullable). **Per-session** because ordering is session context — two sessions may reference the same task with different orders. `tasks.sort_order` is global and won't serve this; project-wide ordering is the separate future `project next`.
- Equal `order` values across tasks = **parallelizable** (safe to run concurrently).
- Migration rides with / after E-1462 (the other pending `session_tasks` column change).

## Command (Claude-callable)
- A verb that sets `session_tasks.order` for the current session.
- Compact spec: whitespace advances the order counter; `|` groups tasks at the SAME order (parallel):
  - `E-100 E-101|E-102 E-103` → E-100=1, E-101=2, E-102=2, E-103=3 (E-101 ∥ E-102).
- Accept JSON as an alternative input form for programmatic use.
- Scope: only the current session's `session_tasks` rows; unknown/foreign task ids rejected.

## Consumed by
- `session next`: sort the do/plan group by `order`; equal order renders as a parallel group.
- `session next --tree` (sibling task): renders the order/parallel structure.

## Out of scope
- Project-wide ordering (`project next`) — later.

## Decisions (resolved 2026-06-29)
- **Column name:** `session_tasks.do_order` (not `order` — reserved word, would need quoting everywhere). Still distinct from global `tasks.sort_order`.
- **Verb:** `endless session order "<spec>"` — new subcommand under the existing `session` Click group, alongside `session status`.
- **Set semantics:** Replace-all. The spec is the complete ordering; any `session_tasks` row for the session NOT named in the spec has its `do_order` reset to NULL. Idempotent / re-runnable.
- **Event-sourced** (not a direct Python write): new event kind `session_tasks.ordered` emitted via `event_bridge.emit_event` → Go executor `execSessionTasksOrdered`, mirroring `session_status.recorded`. Honors the "no Python DB writes" policy. The order data is committed to the shared ledger like other session-scoped events (precedent: `session_status.recorded`), but — like `session_statuses`/`session_tasks` — is a live side-effect, NOT replayed by `rebuild-db`/`ProjectToTempDB`.
- **JSON input form:** array-of-groups, isomorphic to the compact spec — `[["E-100"],["E-101","E-102"],["E-103"]]`. Selected with `--json`.
- **Validation:** task ids must already have a `session_tasks` row for the current session (rejects unknown/foreign ids). Structural errors (malformed id, empty spec/group, duplicate id) are rejected Python-side before any event is emitted; membership errors are rejected in the Go executor (rolls back the DB mutation; the attempt's ledger line is retained as audit, consistent with the events-authoritative generic-update path).
- **`updated_at`:** reordering does NOT bump `session_tasks.updated_at` (it isn't task work; the reap-worktrees staleness clock keys on that column).
