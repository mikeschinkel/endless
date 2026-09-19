You are implementing **E-1390** — add the `endless session tasks` CLI that lists tasks the current (or specified) session has touched. First read-side surface on the events-as-primitive data model (E-1352).

## Dependency note

This task is blocked-by E-1381. E-1381 fixes E-1322's `shouldRecordSessionTouch` guard so `session_tasks` actually populates from real `endless task add` / `endless task update` invocations. Verify E-1381 is `confirmed` and that `XDG_CONFIG_HOME=$HOME/.config endless sql "SELECT count(*) FROM session_tasks"` returns a non-zero count BEFORE writing the CLI. If session_tasks is empty, E-1381's fix hasn't actually shipped end-to-end — surface that and stop.

## What this is

`session_tasks` (E-1322, commit `4652b1a` on main) materializes "session N touched task M." This CLI surfaces that data. Read-only verb. Default session is the current TMUX_PANE's bound session.

```
endless session tasks [OPTIONS]

Options:
  --session N           Session id (default: current pane's session)
  --since <when>        ISO timestamp filter on updated_at
  --limit N             Cap row count (default: 50)
  --format [table|json] Output format (default: table)
```

Output columns: task ID / status / title / updated_at (most-recent touch first).

## Architecture

Per E-894, DB reads live in Go. Python CLI is parse-args + display only.

- **Go side**: add `monitor.RecentSessionTaskTouches(sessionID int64, since string, limit int) ([]SessionTaskTouch, error)` in `internal/monitor/session_tasks.go` (new file). Returns rows joined to `tasks`.
- **Python side**: a new subcommand under `src/endless/session_cmd.py` (or wherever `endless session ...` already lives). Resolves the default session via existing `_current_endless_session_id` helper. Calls the Go helper via `endless-tmux`-style binary OR via the standard CLI bridge — match whatever existing read verbs use.

Check existing `endless session show` (E-991) or similar verbs for the established pattern. Mirror it; do not invent a new bridge.

## Files to touch

- `internal/monitor/session_tasks.go` (new) — query helper + test
- `internal/monitor/session_tasks_test.go` (new) — table-driven tests
- `src/endless/session_cmd.py` or equivalent — new subcommand
- Tests on the Python side that mock the Go bridge if needed
- `docs/guide/sessions.md` if it documents the verb set (sanity check whether E-1349 has reframed it first)

## Pattern to follow

`internal/events/session_tasks.go` (the helper landed in commit `4652b1a`) shows the in-tx dbQuerier pattern. Reads aren't in-tx but the same dbQuerier convention applies for testability. The query:

```sql
SELECT t.id, t.status, t.title, st.updated_at
FROM session_tasks st
JOIN tasks t ON t.id = st.task_id
WHERE st.session_id = ?
  AND (? = '' OR st.updated_at >= ?)
ORDER BY st.updated_at DESC
LIMIT ?
```

## Tests

- Returns empty for session with no touches
- Returns rows in updated_at DESC order
- `--since` filters correctly
- `--limit` caps row count
- JSON format produces parseable JSON
- Unknown session id returns empty (not error)

## E2E verification (critical — don't skip)

After unit tests pass, in this worktree's sandbox:

```
endless task add "probe E-1390"
endless session tasks                   # should show the probe at top
endless session tasks --format json     # JSON well-formed
endless task update <probe-id> --description "x"
endless session tasks                   # updated_at advances
```

This is the gap that let E-1322 land broken — don't trust unit tests alone.

## House rules

- `just build`, not bare `go build`. Binaries → `./bin/`.
- ClearPath style, doterr error wrapping, go-dt for any filepath handling.
- Verb gating: `endless verb add 'tasks' --definition '...'` won't be needed; "tasks" is a noun-as-subcommand, not a title verb. CLI subcommand names aren't gated.
- LESSONS.md "Verify the actor model before specifying actor-based guards" — applies if you touch any guard logic. You shouldn't need to here; this is read-only.

## Closing

- `endless task update E-1390 --status verify --outcome "endless session tasks CLI surfaces session_tasks data; --since/--limit/--format flags; e2e verified."` — `verify`, not `completed` or `assumed`.
- Handoff message: "To verify: `endless task add 'throwaway'`, then `endless session tasks` shows it at top."
- Do NOT auto-land. Ask Mike.
