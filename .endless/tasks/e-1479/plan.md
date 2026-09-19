# E-1479 — `--record-only` flag for `endless worktree land`

## Context

`task_landings` projects `task.landed` events emitted by `endless worktree land`. Lands that happened outside that flow — manual `git merge`, or before the table existed (E-1402, landed 2026-05-17) — have no sanctioned way to be registered after the fact; a direct INSERT would bypass the event-sourced contract. `--record-only` lets the existing `land` verb emit a `task.landed` event for an already-completed land while skipping all rebase / dedup / orphan-drop / ff-merge work. First use case: backfill the missing `task_landings` row for E-1402.

Flag (not a sibling `record-landing` verb): two-segment verbs are a CLI smell here, and a flag is more discoverable via `land --help`.

### Why `task_landings.branch` stays (with a fix)

Confirmed `branch` lives on `task_landings`, not `sessions`. Its **only** non-test consumer is the worktree reaper (`internal/monitor/reap_worktrees.go:115-154`): it reads the most-recent landing's `branch` and runs `git branch -D <branch>` during TTL cleanup (introduced by E-1337, commit `832b7aa`). So `branch` is load-bearing for a **live** land, but worthless for a record-only of an already-reaped branch (E-1402) — nothing to clean up. NULL is the correct value there. The column is `TEXT NOT NULL` today (written against instruction); it must accept NULL.

## Sequencing — HELD until E-1486 lands

Per decision: the record-only DB reads go in **Go**, not Python `db.query`, and E-1479 waits for **E-1486** (the Python→Go DB-access port, in flight in another session) so its Go reads build on that layer. Do not implement now. E-1479 is set `blocked` with a `blocked_by` dependency on E-1486. The Go DB API specifics are deferred until E-1486 exists — this plan fixes *what* reads are needed, not their exact Go signatures.

## Design

### Auto-discovery (record-only, queried against the **main** checkout)

`branch`/`sha` cascade:
1. **Primary** — `git for-each-ref --format='%(refname:short) %(objectname)' refs/heads/task/<id>-*`, drop tuples whose tip is already in `task_landings` for this task. Exactly one left → use its branch + tip sha. Multiple → refuse, listing candidates. Zero → fall through.
2. **Fallback** — `git log <base> --grep '^E-<id>:' --format='%H %cI'`, drop already-recorded shas, pick the newest remaining as `sha`; `branch` is left empty → NULL (a deleted branch name is unrecoverable: commits don't store it, ff-only merges leave no merge-commit message, the ref's reflog is gone).
3. **Hard error** — neither path yields a candidate; message names what was searched.

`--sha` supplied → skip the cascade; `branch` is empty → NULL unless `--branch` given (the E-1402 path).
`landed_at` ← chosen commit's `%cI` (overridable by `--at`). `session_id` ← resolver/current session (overridable by `--session-id`).

`branch` is recorded only when the ref is still alive (sole benefit: later reaper cleanup); this rides for free on the SHA discovery the cascade already does.

### Flags

- `--record-only` — bare flag, enables the mode.
- `--sha <hash>` — override sha; skips cascade.
- `--branch <name>` — override branch; `--branch ''` records NULL deliberately.
- `--at <iso>` — override `landed_at`. Parse with `datetime.fromisoformat(at.replace('Z','+00:00'))` (matches `session_cmd.py:976`); normalize to offset-bearing (naive → `.astimezone()` for local offset) before handing to the Go binary.
- `--session-id <id-or-uuid>` — override resolver; integer → `sessions.id`, else exact/prefix `sessions.session_id` (logic mirrors `_resolve_session`, session_cmd.py:61, reimplemented as a Go read post-E-1486). Miss → hard error naming the value.
- Any of `--sha/--branch/--at/--session-id` without `--record-only` → hard error.
- `--record-only --dry-run` → print planned (task_id, branch, sha, landed_at, session_id, and auto-discovered/override per field); emit nothing.

### Branch nullability + reaper fix

- `internal/schema/schema.sql` — `branch TEXT NOT NULL` → `branch TEXT` (fresh builds).
- `internal/schema/changes/e-1479-task-landings-branch-nullable.sql` (new) — rebuild live table to nullable `branch`: CREATE `task_landings_new` (same cols/FKs, `branch TEXT`), `INSERT … SELECT *`, DROP, RENAME, recreate `idx_task_landings_task`. Mirrors `e-1459-reshape-schema-version.sql`; runs in the dispatcher's `BEGIN IMMEDIATE` (nothing references `task_landings`, so FK-safe). Auto-applied at land time by the `just land` recipe (Justfile:134) before the ff-merge.
- `internal/events/projector.go` `replayTaskLanded` (and `executor.go execTaskLanded`) — insert SQL NULL when payload `branch` is empty (`var b any = p.Branch; if p.Branch=="" { b = nil }`). Normal lands always pass a real branch → unchanged.
- `internal/monitor/reap_worktrees.go:115-154` — **required by the above**: scan `branch` into `sql.NullString`; only `git branch -D` when `branch.Valid && branch.String != ""`. A NULL into a plain `string` errors and aborts the reap.

### `landed_at` is UTC; `--at`/commit-date mechanism

`evt.TS` is kairos base32, not raw ISO: `Event.Validate()` runs `kairos.Parse(e.TS)` and the projector renders `landed_at` via `kairosToISO(evt.TS) = …Physical().UTC().Format("2006-01-02T15:04:05")` (projector.go:418). So an override is a kairos timestamp and `landed_at` is always UTC (consistent with every existing row — confirmed decision).

- `cmd/endless-event/main.go` — `runEmit`/`run` gain `--ts <iso>`; when set, `time.Parse(time.RFC3339, …)` → `kairos.New(parsed, 0, nid)` (timestamp.go:26) replaces `clock.Now()`. Downstream (envelope `ts.String()`, validation, projector) unchanged. Malformed → error.
- `src/endless/event_bridge.py` — `emit_event` gains `ts: str|None=None`; when set, appends `--ts <ts>`. Default path byte-for-byte unchanged.
- Record-only path passes offset-bearing ISO for `ts` (auto: `%cI`; `--at`: normalized as above).

Consequence: E-1402's `2026-05-17T22:48:59-04:00` lands as `landed_at = 2026-05-18T02:48:59`.

### Idempotency

Refuse on duplicate `(task_id, sha)` in `task_landings`; error names the existing row's `id` + `landed_at`. Re-landings (advanced sha) still work.

### Success output (one physical line for copy-paste)

```
• Recorded landing of E-NNNN (branch <name-or-(null)>, sha <short>) at <landed_at>
```

### Help text

`--record-only`: "Register a task.landed event for a land that already happened; skip rebase/ff-merge. Auto-discovers branch/sha/landed_at from local git; override with --branch/--sha/--at if needed."

## Files to modify

- `src/endless/cli.py` — `worktree_land` gains the five flags + flag-without-mode validation.
- `src/endless/worktree_cmd.py` — early `record_only` branch in `land_worktree` needing no on-disk worktree: auto-discovery, dedup guard, session-id resolution, dry-run; `_record_landing` extended with optional `ts` + empty-branch-as-NULL (or sibling helper). Normal flow unchanged.
- `src/endless/event_bridge.py` — `emit_event` `ts` passthrough.
- `cmd/endless-event/main.go` — `--ts` flag.
- `internal/schema/schema.sql` — `branch` nullable.
- `internal/schema/changes/e-1479-task-landings-branch-nullable.sql` (new) — live-table rebuild.
- `internal/events/projector.go`, `internal/events/executor.go` — empty branch → NULL insert.
- `internal/monitor/reap_worktrees.go` — NULL-safe branch read.
- **Go DB reads (post-E-1486):** dedup guard, auto-discovery "already-recorded sha" filter, `--session-id` resolution — implemented against E-1486's Go DB layer.
- `tests/test_worktree_land_record_only.py` (new); `tests/test_event_bridge.py` (`ts` passthrough regression); Go tests for `endless-event --ts` and the projector empty-branch→NULL + reaper NULL-branch path.

## Out of scope

- Backfilling historical rows beyond E-1402; touching the normal land flow; restructuring `task_landings` beyond branch nullability; `--record-only` on other commands.

## Verification (from the main checkout after E-1479 lands; `just land E-1479` auto-applies the schema change)

1. **E-1402 backfill:** `endless worktree land E-1402 --record-only --sha 33a2ea48ef1a2cfefdd9d5d86e1eef5a410434b5`, then `endless sql "SELECT * FROM task_landings WHERE task_id = 1402"` → one row: that sha, `branch IS NULL`, `landed_at = 2026-05-18T02:48:59`, `session_id` = the confirming session.
2. **Schema:** `PRAGMA table_info(task_landings)` → `branch` `notnull = 0`.
3. **Reaper NULL-safe:** a NULL-branch landing whose worktree dir still exists is reaped without error (worktree removed, branch deletion skipped).
4. **Live land + retry:** land a test task normally; re-run `--record-only` without `--sha` → duplicate-guard refusal.
5. **Auto-discovery happy path / fallback / hard error / dry-run / flag-without-mode / session-id lookup** — per the flag matrix above.
6. `just test` (Python) + `just test-go` (Go) green before `verify`.

## Closing

- This session does **not** implement. E-1479 is set `blocked` with a `blocked_by` dependency on E-1486.
- When E-1486 lands, a follow-up session implements against the Go DB layer, then flips to `verify` with the E-1402 backfill command in the handoff. Do not auto-land.
