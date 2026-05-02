# Plan — E-1011: Backfill companion file on UserPromptSubmit

## Context

E-989 wrote per-session companion files only on `SessionStart`. Every Claude session that was already running when E-989 landed (which is most of them, including the one we built E-989 in) has no companion file, so `endless session cd` (E-990) can't see it. Yesterday we hand-wrote the JSON to make this session's file appear; that's an unacceptable workaround for everyone else. The fix mirrors the existing `BackfillProcess` pattern in `cmd/endless-hook/claude.go`: on `UserPromptSubmit`, if the companion file is missing, write it. Pre-E989 sessions then self-heal on the next user message.

## Approach

**Worktree** — `.endless/worktrees/e-1011`, branch `e-1011-backfill-companion`, from `main`.

**Three small changes:**

1. **`internal/monitor/session.go`** — extend `SessionInfo` with `StartedAt string`; add `started_at` to the `SELECT` and `Scan` in `GetActiveSession` (line 102-106). All other callers inherit the field for free; nobody else needs updating.

2. **`internal/monitor/companion.go`** — add `CompanionExists(projectID int64, harness, sessionID string) (bool, error)`. Wraps `CompanionPath` + `os.Stat`; treats `ErrNotExist` as `(false, nil)`. Hides path construction from the call site.

3. **`cmd/endless-hook/claude.go`**:
   - `writeClaudeCompanion` (line 649): use `session.StartedAt` (now available on `SessionInfo`) instead of `time.Now().UTC().Format(...)`. Fall back to current time only if the DB row's `started_at` is empty (defensive, shouldn't happen post-`InitSession`). This makes backfilled and freshly-written files identical for the same session.
   - `UserPromptSubmit` handler (after the `BackfillProcess` call at line 135): check `monitor.CompanionExists(projectID, "claude", payload.SessionID)`; if false, call the existing `writeClaudeCompanion(projectID, payload)`. Fatal on error — same policy as `BackfillProcess` and as `SessionStart`'s companion write.

## Reused functions

- `writeClaudeCompanion` — `cmd/endless-hook/claude.go:649` — already does exactly the right thing once `StartedAt` is sourced from the DB.
- `monitor.WriteCompanion` — `internal/monitor/companion.go:55` — atomic write, overwrite-OK semantics; called only when missing.
- `monitor.GetActiveSession` — `internal/monitor/session.go:95` — already invoked by `writeClaudeCompanion`; gains the `StartedAt` field with no caller updates required.

## Critical files

| File | Change |
|---|---|
| `internal/monitor/session.go` | Add `StartedAt` field + scan it |
| `internal/monitor/companion.go` | Add `CompanionExists` |
| `internal/monitor/companion_test.go` | Add tests for `CompanionExists` (present + missing) |
| `cmd/endless-hook/claude.go` | Use `session.StartedAt` in `writeClaudeCompanion`; backfill block in `UserPromptSubmit` handler |

## Verification

**Unit tests** (in worktree, before any hook deployment):
- `go test ./internal/monitor/...` — new tests for `CompanionExists` (file present → true; absent → false; bad project id → error). Existing companion tests must still pass.

**Smoke test**:
- `just build` in worktree.
- Synthetic stdin payload to `bin/endless-hook claude` with a `UserPromptSubmit` event for a session whose companion file does not exist; assert the file appears post-invocation.
- Same payload again; assert the file is not modified (mtime unchanged within a 1s window) — the `CompanionExists` short-circuit works.

**End-to-end** (after landing to main + `uv tool install --reinstall .`):
- Delete this session's companion file (`rm .endless/sessions/claude-f41f263e-*.json`).
- Submit any prompt in this Claude session.
- Verify the file reappears with correct fields, including `started_at` matching the DB's record (not "now").

**Out of scope**:
- Changing the SessionEnd remove path (already correct).
- Cleaning up stale companion files at hook-time (E-990's reader handles that lazily).
- Any change to E-989's writer or its tests beyond the `StartedAt` source.
