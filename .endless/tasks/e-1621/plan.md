# E-1621 — `endless agents`: list bg agents scoped to the active epic

## Context
Claude Code's native Agent View is global — it can't filter background agents by
the epic you're coordinating. E-1621 adds a plain-text `endless agents` command
as the epic-scoped supplement (the TUI version is the `later` task E-1622).

The description specifies the query: background-agent sessions
(`kind_id=background`, `state='working'`) filtered by the resolved epic, printing
`id / short_id / task / title / started_at`. `--epic E-NNNN` overrides the
auto-resolved epic; `--all` drops the epic filter (project-scoped).

### Investigation finding (drives the design)
`active_epic_id` is written **only** for background-agent rows
(`RecordBgAgentSession`, session.go:341). Interactive/coordinator sessions —
the ones a person runs `endless agents` from — always have `active_epic_id NULL`,
because the claim handler `execTaskClaimed` (executor.go:758) sets only
`active_task_id`. E-1571 *intended* coordinator rows to carry the epic but
explicitly deferred that write to "the day-to-day claim flow," and it was never
implemented. Same gap also keeps the E-1571 status-line `[E-EEEE:E-CCCC]` prefix
from ever rendering for interactive sessions.

**Decisions (Mike):**
- Fill that gap **first**, in its own task/session; E-1621 then reads
  `active_epic_id` **literally**. → File the gap-fill task (cleans up E-1571);
  Mike spawns it separately. Block E-1621 on it.
- `--all` = **current project only** (project-scoped, not global).
- Interactive run with no `--epic`, no `--all`, no resolvable epic → **error
  with guidance** ("pass --epic E-NNNN or --all").

## Architecture
User surface is Python `endless agents`; the DB read is Go (no new Python DB
reads, per E-1486). Python resolves the current session id and shells to a new
`endless-go session-query list-bg-agents` verb that does the query and returns
JSON; Python formats plain text.

## Changes

### Go — `internal/monitor/bg_agents.go` (new)
- `type BgAgent struct { ID int64; ShortID string; TaskID *int64; Title string; StartedAt string }` (json-tagged).
- `ListBgAgentsForEpic(epicID int64) ([]BgAgent, error)` — `WHERE kind_id=background AND state='working' AND active_epic_id=?`, LEFT JOIN tasks for title, `ORDER BY started_at`.
- `ListBgAgentsForProject(projectID int64) ([]BgAgent, error)` — same but `project_id=?` (the `--all` path).
- `SessionActiveEpic(sessionID int64) (*int64, error)` — literal read of `active_epic_id` (nil when NULL / no row).
- Use `sessionkind.SessionKindBackground`, not a literal 2.

### Go — `internal/sessionquerycmd/session_query.go`
- Add `list-bg-agents` case + `runListBgAgents`; update `usage()`.
- Flags: `--session-id`, `--epic-id`, `--all`, `--project-root` (exactly one of the first three; `--all` needs `--project-root`).
- Returns JSON object: `{"scope":"epic"|"all","epic_id":<id|null>,"agents":[...]}`. For `--session-id`, resolves epic via `SessionActiveEpic`; null epic ⇒ `epic_id:null, agents:[]`.

### Python — `src/endless/agents_cmd.py` (new) + `cli.py`
- `@main.command("agents")` with `--epic` (TASK_ID type) and `--all`.
- Logic module (mirrors epic_cmd.py thin-wrapper style): validate not-both;
  resolve via `_current_endless_session_id` when neither flag; `config.require_db_context()` then shell to the Go verb with `config.go_db_context_args()`; parse JSON; format.
- Errors (Q3): no current session → guidance; scope==epic and epic_id null → guidance.
- Plain-text table: `Background agents under E-NNNN (N):` header, columns ID/SHORT/TASK/STARTED/TITLE; empty-state line.

## Tests
- Go: `internal/sessionquerycmd/list_bg_agents_test.go` — seed temp DB (bg agent under epic, a tmux session, an ended bg agent, a bg agent in another epic), invoke binary, assert JSON for `--epic-id`, `--session-id` (incl. null-epic), `--all`. Mirror `list_live_test.go`.
- Python: `tests/test_agents_cmd.py` — seed sessions+tasks, invoke `endless agents --epic`, assert rows + empty-state + both-flags error. Mirror `test_epic_cmd.py`.
- Per-task: `tests/tasks/e-1621-verify.sh` (sandbox DB; assert_succeeds/assert_refused), modeled on the E-1577 script.

## Follow-up task (filed, Mike spawns)
"Populate sessions.active_epic_id on interactive claim of an epic-descendant
task" — `--cleans-up E-1571`, `--relates-to E-1621`, `--parent E-1537`. Make
`execTaskClaimed` set `active_epic_id = nearestEpicAncestor(taskID)` (clear on
release / non-epic re-claim). Unblocks E-1621's interactive auto-resolve and the
status-line epic prefix. **E-1621 is blocked-by this task.**

## Verification
- `just build` then `just test` (Python) and `go test ./internal/...` (Go) green.
- `tests/tasks/e-1621-verify.sh` green against a sandbox DB.
- `--epic`/`--all` work immediately; interactive auto-resolve lights up once the
  gap-fill task lands.
