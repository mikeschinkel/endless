# E-894 — Move task-display DB reads from Python to Go

> Refreshed 2026-05-26 against current `main` (HEAD survey). Supersedes the
> 2026-05-02 plan, whose "focus pilot" template and `migrate.go` framework
> assumptions are stale (see Reconciliation below).

## Goal

Move every task-display DB **read** out of Python and into Go, so Python has
zero SQLite knowledge for task operations. Python keeps rendering (Rich tables,
markdown, tree, `--json`, `--llm`); only the data-fetch step moves. Decoupling
scope: **full** (Phases 1-3) per decision with Mike 2026-05-26 — also retire
Python's schema bootstrap so Go owns DB creation.

## Reconciliation with current main (what changed since 2026-05-02)

- **No "focus pilot" to mirror.** The old plan said it would copy an established
  `query_bridge.py` + `internal/events/query.go` + `endless-event get-focus`
  read pattern. That code was never landed (stranded uncommitted in an orphaned
  `e-1031` worktree) and is abandoned. We build the read pattern **fresh**,
  mirroring the **write** bridge (`event_bridge.py`) instead. E-1053, which
  formalized that pilot, is obsoleted.
- **Schema framework is gone.** E-1459 deleted the E-863 `migrate.go` versioned
  framework and replaced it with `internal/schema/schema.sql` (authoritative,
  applied by Go on every `monitor.DB()` open) + one-shot `internal/schema/changes/*`.
  The forward schema is **already Go-owned**. So the old "port V5/V6 migrations
  into Go" work is **moot**; Phase 3 is now pure deletion of Python's bootstrap,
  not migration porting. (No migration logic is written — none is needed.)
- **Read-site drift:** all read functions still exist; `_project_root_for_task`
  is now `_main_root_for_task` (`task_cmd.py:373`); the `session_cmd.py:1300`
  task read is now `:1434`. `suggestions_cmd.py` has **no** task reads — it drops
  off the worklist.
- **`endless-event` uses raw stdlib** `switch os.Args[1]` + per-subcommand
  `flag.NewFlagSet` (no go-cliutil); first call is `monitor.ConsumeDBContextFlag()`.
  New read subcommands follow that same internal-binary style.

## The pattern (mirror the write bridge)

Writes today: `event_bridge.py` → `shutil.which("endless-event")` +
`config.require_db_context()` + `*config.go_db_context_args()` (the E-1429
`--config-dir` threading) → `subprocess.run(capture_output, text)` →
`json.loads(stdout)`. Reads mirror this exactly via a new `query_bridge.py`.

**CRITICAL:** the read bridge MUST inject `*config.go_db_context_args()` right
after the binary name, exactly as `event_bridge.py:135-137` does, or reads trip
the E-1429 worktree DB-context gate (`monitor.guardWorktreeDBContext`).

**JSON I/O convention:**
- Single-row query → one JSON object on stdout, or literal `null` if not found.
- List query → JSON array, or `[]` if empty.
- Error → stderr prefixed `endless-event: error:`, exit 1 (exit 2 for missing
  required flags).
- Output shapes must carry **every field** Python's default / `--json` / `--llm`
  formatters consume today (capture per-command during Phase 1).

**Phase 1 subcommand surface:**

| Subcommand | Replaces (Python) | Output |
|---|---|---|
| `get-task --id N` | `detail_item` core query | object/null |
| `list-tasks [filters]` | `show_plan` | array |
| `next-tasks [filters --limit N]` | `next_tasks` | array |
| `search-tasks --query S [...]` | `search_tasks` | array |
| `task-children --id N` | `detail_item --children` | array |
| `task-relations --id N [--rel-type T]` | `get_all_relations` / `_related_task_ids` / `show_relations` | array |
| `active-tasks` | `active_tasks` | array |
| `recent-tasks [--limit N]` | `recent_tasks` | array |
| `list-decisions [filters]` | `list_decisions` | array |
| `task-by-source-file --path P` | `_main_root_for_task` SQL | object/null |

Filters mirror the Click flags: `--phase --status --tier --parent --related-to
--rel-type --all --sort --limit`.

## DRY sources (lift, don't rewrite)

- `internal/web/queries.go`: `GetProjectTasks` (≈ `show_plan`: tree + child_count
  + blocked_by GROUP_CONCAT), `GetCurrentWork` (≈ `active_tasks`),
  `GetProjectDependencies` (≈ `get_all_relations`), plus group/detail helpers.
  These bind to web `data.*` view structs — extract the SQL/scan into a shared
  read helper, then have web reuse it.
- `internal/monitor/task.go`: `GetActiveTasks`, `TaskText`, `GetProjectName`.
- Go DB handle: `monitor.DB()` (`internal/monitor/db.go:252`); path honors the
  E-1429 dbContextDir then XDG.

## Phases (each is a child task; gate on prior phase landing; own worktree each)

1. **Add Go read subcommands to `endless-event`** (child task). Go read helpers +
   the 10 subcommands above; lift overlapping SQL from `web/queries.go`; per-helper
   tests. No Python change.
2. **Add `query_bridge.py` + cut every task read over** (child, blocked by P1).
   New bridge mirroring `event_bridge.py` (incl. `--config-dir`); replace each
   `db.query` task read; delete the dead Python SQL bodies; golden-output parity.
3. **Remove Python's schema bootstrap** (child, blocked by P2). Delete
   `_init_schema`/`_migrate*` from `db.py`; Go owns DB creation; subsumes the
   E-1116 empty-DB crash. **Scope note:** db.py's `query()` stays — Python still
   reads non-task tables (sessions/projects/suggestions); those are out of scope.
   "Python has zero SQLite knowledge" applies to **tasks**, not all tables.

## Out of scope

- Web/TUI reads (already Go). P1 lifts shared SQL so web reuses it; no UI cutover.
- Non-task table reads (sessions/projects/suggestions) — separate future tasks.
- Goal/focus subsystem — entirely unbuilt on main; E-894 has zero dependency.
- Any migration logic (none needed; forward schema already in `schema.sql`).

## Decision log

- 2026-05-26 (Mike): **full decouple, Phases 1-3** — the schema phase is cheap
  now that E-1459 moved forward schema to Go; do it rather than leave Python a
  SQLite foothold.
- 2026-05-26 (Mike): **start fresh, no salvage** of the stranded `e-1031` pilot.
- 2026-05-26: reconcile-then-spawn; this session does DB-only planning (no claim,
  no worktree); implementation spawned per-phase.

## Closeout (E-894 has no code of its own)

E-894 is the umbrella; all implementation lives in the phase children. Endless
has **no automatic parent rollup** — confirming the children does NOT flip the
parent. So once **E-1481, E-1482, and E-1484 are all confirmed**, set
**E-894 → verify** (`endless task update E-894 --status verify`); Mike confirms
E-894. The terminal phase (E-1484) carries the same reminder so the spawned
session that finishes the chain does the flip.
