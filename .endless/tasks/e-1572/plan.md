# E-1572 — Add soft warning at endless task spawn --bg when N bg agents already active

Lands after **E-1568** (background-kind rows exist) and **E-1571** (`kind_id` FK column populated). Independent of E-1567/E-1570.

## Decisions

### Threshold and config
- Default: `3`. Configurable as `bg_throttle_warn` in `.endless/config.json`.
- Config loader: read at spawn time via `config.project_config_read(config.resolution_cwd())` (Python). Pure read; no write path.
- Disable mechanism: set `bg_throttle_warn` to `0` (or any value `<= 0`) — the warning fires only when `active_count >= threshold AND threshold > 0`.

### Count query — lives in Go, NOT Python
Per E-1486 / E-894, new DB reads go through the Go `session-query` helper, not Python `db.query`. (The package docstring states this is its purpose; the sibling `_lookup_bg_short_id` predates the rule and is not a precedent to extend.)

- New Go subcommand: `endless-go session-query count-bg-agents --task-id <id>` → prints the integer count.
- Backed by `monitor.CountActiveBgAgents(taskID)`, which resolves `project_id` from the task Go-side (mirroring `RecordBgAgentSession`, so the Python flow needs no DB read) and runs:

```sql
SELECT count(*) FROM sessions
WHERE kind_id = ?            -- int64(sessionkind.SessionKindBackground)
  AND state = 'working'
  AND project_id = ?
```

The `kind_id` filter uses the typed `sessionkind.SessionKindBackground` constant rather than a hardcoded integer or a `session_kinds` subselect — the Go enum is the stable reference. Scope is **per project**, not global: bg agents in unrelated projects do not consume this project's mental budget. `tasks.project_id` is `NOT NULL`, so the resolution is a plain scan.

### Trigger and behavior
At dispatch time, AFTER status gate / worktree pre-claim but BEFORE invoking `claude --bg` (in `_spawn_bg_dispatch`, immediately before the `claude --bg` subprocess):
- Call `_bg_throttle_warn(item_id)`: read the threshold from project config, get the count from the Go helper.
- If `active_count >= threshold` (and `threshold > 0`), emit a 3-line warning to **stderr** via `click.echo(..., err=True)` (not stdout — keeps short-id parsing on stdout clean if a caller pipes us):

```
warning: {active_count} bg agents already active for this project (threshold: {threshold}).
  Each bg agent consumes a parallel-execution slot; quota burns ~linearly.
  Community-observed sweet spot is 3–5 parallel agents. (Configure via .endless/config.json:bg_throttle_warn.)
```

- **Does not block.** Proceed with dispatch unconditionally. Coordinator decides.
- **Advisory-only robustness:** `_bg_throttle_warn` never raises on its own failure — a config or count-helper hiccup (non-zero exit, unparseable output) is swallowed silently so it can never abort the spawn.
- No prompt-for-confirmation (would break automation; soft warning is the point).

### Rationale (for the implementer)
Per `docs/research-2026-06-12-claude-background-agents.md` §7 — bg quota burns linearly with parallel count; community sweet spot is 3–5. Threshold default of 3 leaves headroom before hitting the upper community-observed limit while warning early.

### Suppression
No `--no-warn` flag. If automation needs silence, set `bg_throttle_warn` to `0` in `.endless/config.json`. Per-invocation suppression would just be reinventing log-level filtering.

## Files

### Changed (Go)
- `internal/monitor/session.go` — add `CountActiveBgAgents(taskID int64) (int64, error)`.
- `internal/sessionquerycmd/session_query.go` — add the `count-bg-agents` subcommand (`runCountBgAgents`) + usage line.

### Changed (Python)
- `src/endless/task_cmd.py` — add `_bg_throttle_warn(item_id)` (threshold read + Go-helper count + stderr warning) and call it inside `_spawn_bg_dispatch`, between pre-claim and the `claude --bg` subprocess call.

### Tests
- `internal/monitor/count_bg_agents_test.go` (new) — count-query correctness: scopes to project + kind=background + state='working'; excludes ended sessions, foreground sessions, and other projects; returns 0 when none active.
- `tests/test_spawn_bg_throttle.py` (new) — warning-decision + text (count helper mocked):
  - Threshold 3, 2 active → no warning.
  - Threshold 3, 3 active → warning emitted to stderr.
  - Threshold 3, 5 active → warning emitted once (not per-overage).
  - Threshold 0 / negative → no warning regardless of count.
  - Unset key → default 3.
  - Warning text contains the count and threshold.
  - Count-helper non-zero exit → silent (no warning, no raise).
- `tests/test_spawn_bg.py` — existing dispatch-mechanics tests stub `_bg_throttle_warn` to a no-op so the new pre-dispatch count shell-out doesn't perturb them.

## Sequence note

Lands after **E-1568** (the rows it counts didn't exist before).

## Verification

1. `just test` (Python) and `go test ./internal/monitor/ ./internal/sessionquerycmd/` pass.
2. `bin/endless-go --config-dir ~/.config/endless session-query count-bg-agents --task-id <id>` prints an integer.
3. Set `bg_throttle_warn: 1` in `.endless/config.json`. Dispatch one bg agent → no warning. Dispatch a second → warning text appears on stderr, dispatch still succeeds.
4. Unset / set to 0 → no warning at any count.
