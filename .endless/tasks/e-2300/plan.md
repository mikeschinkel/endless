# Plan — recognise sessions on a vanished tmux server as dead

Decided with Mike, 2026-10-11 to 2026-10-17 (/needs-plan). Rationale is in `analysis`.

## 1. Record which socket each server lives on

- New table `tmux_servers (server_uuid TEXT PRIMARY KEY, socket_path TEXT NOT NULL, first_seen_at TEXT NOT NULL)`, added by the next goose migration and mirrored byte-for-byte in `internal/schema/schema.sql` (TestMigrate_MatchesSchemaSQL).
- Written where bindings are made, `EnsureTmuxPaneProcess` in `internal/monitor/processes.go`: alongside `TmuxServerUUID()`, read the same server's `#{socket_path}` and upsert (uuid, socket path). Insert-or-ignore: a uuid names one server lifetime, so its path never changes.

## 2. Judge a recorded server gone

In `internal/monitor/liveness.go`, after `observeTmuxServers`: for each tmux binding whose `server_uuid` was not observed and has a `tmux_servers` row, probe that row's socket path directly.

- Something answers at the path with a different `@server_uuid`, or with none → that server is gone.
- The socket file exists but nothing is listening (connection refused) → gone.
- The socket file is missing → no verdict (`unknown`): a server can outlive its socket file (the macOS `/tmp` cleanup).
- Any other failure (timeout, permission) → no verdict.

Gone servers go into a new TEMP table beside `observed_servers` (same connection-scoping rules), and the `session_liveness` view reads a binding on a gone server as `dead`. Nothing is written to `sessions` from a verdict: read time only (E-1898's rule stands).

## 3. One test for "present"

- Stewardship (`presentSession` in `internal/monitor/session_stewardship.go`, from E-2293) switches from `sl.liveness = 'live'` to `sl.liveness != 'dead'`, the claim guard's test (`ListLiveSessions`).
- These readers, which check `sessions.state` only, also require `liveness != 'dead'`: `WorktreeInUse` (`internal/monitor/worktree_inuse.go`), the two `in_flight` expressions in `internal/monitor/session_status.go`, and the live-session EXISTS in `internal/monitor/session_graph.go`'s node query.
- Pane-to-session lookups (`tmux_lookup.go`, `events/session_status.go`, the dedup in `session.go`) already match the current server's uuid; unchanged.

## 4. Recovery from an `unknown` session

**The claim/spawn refusal** (`_check_task_ownership` in `src/endless/task_cmd.py`), when the blocking session reads `unknown`, says its tmux server cannot be reached and offers, in order: `endless start` (when the session's recorded socket is one of Endless's and that server is not running), `endless session resume ES-N` to continue it, and `endless session end ES-N` to discard it. The `live` case keeps today's text.

**`endless session end <ref>`** (new; ref as `session resume` takes it):
1. Refuses a session that reads `live`.
2. Refuses a session whose `last_activity` is within the last 10 minutes unless `--force` (a running Claude fires hooks).
3. Shows the session's task, last activity and liveness and asks y/N; `--yes` skips the prompt. With no terminal to ask on and no `--yes`, it refuses.
4. Emits the existing `session.ended` event through `endless-go event emit`.
5. Prints: "Ended ES-N (E-M). Its transcript is kept. To get it back, run `endless session resume ES-N`. Until then, E-M can be claimed by another session."

## 5. Existing rows

Sessions bound before this lands have no `tmux_servers` row, so they are never judged gone; they keep reading as today (`unknown` once their server is gone) and are cleared with `session end`.

## Tests

Go: fixture tables and a stubbed socket prober, no real tmux — different uuid → dead; no uuid → dead; refused → dead; missing file → unknown; timeout → unknown; observed → live; binding with no `tmux_servers` row → unknown; `EnsureTmuxPaneProcess` records the path; stewardship counts an `unknown` session; the four state-only readers ignore a `dead` session. Python: the `unknown` refusal text; `session end` refusals (live, recent, no tty without `--yes`), prompt, `--force`, `--yes`, and the printed recovery line.
