# E-1426 — Retire `.endless/sessions/*.json` companion files

## Context

`internal/monitor/companion.go` writes a per-Claude-session JSON file at `<projectRoot>/.endless/sessions/claude-<harness_uuid>.json` on every SessionStart / UserPromptSubmit / post-task-mutation, and removes it on SessionEnd. The file carries `harness`, `harness_session_id`, `endless_session_id`, `pane_id`, `cwd`, `pid`, `started_at`, `worktree_path`. Python CLI code globs that directory via `_read_live_companions` (`session_cmd.py:1000`) to answer "which pane belongs to which endless session" and similar questions.

Every field is now either already in the `sessions` DB row (`session_id` is the harness UUID, `id` is the endless_session_id, `process` is the pane id) or derivable on demand (`cwd` via `tmux display-message`, `worktree_path` via `WorktreePathForTask`). The file has accumulated three real failure modes that the DB-only path will fix:

- **E-1408** — `SetProcess` skips empty `TMUX_PANE` at SessionStart, leaving `process` NULL silently.
- **E-1419** — A session started in a worktree writes its companion under the worktree's `.endless/sessions/`, and main-DB resolution from a worktree cwd walks past it.
- **E-1422** — A pane with no companion has no recovery path, blocking CLI use after the E-1401 gate.

E-1426 replaces the file with idempotent UPSERTs on the `sessions` table fired by every Claude hook event. This is the single change that subsumes all three.

## Architecture decisions (Mike, 2026-05-20)

These describe the post-E-1426 architecture; they are not implementation-window choices.

### D1. The `sessions` row is the authoritative session record, written by the Go hook on every event, keyed by harness UUID

The Go hook binary writes the DB directly via `monitor.*` (the hook already opens the DB every event, so this is an incremental extension, not a new architecture). Every Claude hook event — SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, ExitPlanMode, Stop, PreCompact, SessionEnd — fires a single UPSERT helper. The UPSERT is keyed on `sessions.session_id` (the harness UUID, already `UNIQUE`); `process` stays a mutable attribute, not part of identity. Keying on `process` would destroy durable rows; composite keys would multiply rows per session. As part of the same transaction, when a new non-empty `process` value conflicts with a different live `session_id`, the prior occupant is marked `ended` (collision invalidation). One code path means it is self-healing: a NULL `process` from a missed initial value is repaired within one tool call.

### D2. Liveness is `state != 'ended'` plus opportunistic dead-pane reaping; crash detection is explicitly not instrumented

The DB does not store `pid` or `start_time`. A row is "live" when its `state` is not `'ended'`. State transitions are driven by lifecycle hooks (`BindSessionToTask` → `working`, `IdleSession` → `idle`, `EndSession` → `ended`) plus the collision invalidation in D1. For unclean termination where `SessionEnd` never fires (pane closed, server reboot, etc.), an opportunistic reaper at SessionStart shells `tmux list-panes -a` and marks rows whose tmux-format `process` is no longer alive as `ended`. Crashed-Claude-but-pane-alive (the only failure mode the old `pid`-alive check uniquely caught) is explicitly out of scope; revisit only if it surfaces as a real problem.

### D3. The schema is platform-agnostic; platform-specific operations are scoped to named helpers

`sessions.process` stays TEXT and stays named `process`. It already carries non-tmux values (`endless-channel/main.go:60` writes `pid:<ppid>`) and may carry more in the future. `TouchSession(sessionID, platform, process, projectID)` takes `platform` as a parameter so a future non-Claude hook calls the same helper with its own platform string. Platform-specific operations are isolated to explicitly-named helpers: `ReapDeadTmuxPanes` only touches rows whose `process` matches the tmux `%`-pane pattern; `_tmux_pane_cwd` is the tmux-only cwd resolver invoked when `process` is `%`-form. `cwd` itself is derived on demand, not stored — recovered via `tmux display-message -p '#{pane_current_path}'` for tmux sessions, returning `None` otherwise. A future non-tmux Claude or non-Claude harness adds its own reaper and cwd helper without rewriting the per-platform code paths or the `sessions` schema.

## Implementation

### Go side

**`internal/monitor/companion.go`** — delete `CompanionFile` struct, `WriteCompanion`, `RemoveCompanion`, `writeCompanionAtRoot`, `removeCompanionAtRoot`, `companionPathInDir`, `companionDirAtRoot`. Keep `WorktreePathForTask` (still needed by hook worktree-gate logic); move to `internal/monitor/worktree.go` if `companion.go` ends up empty.

**`internal/monitor/session.go`** — add `TouchSession(sessionID, platform, process string, projectID int64) error`. Single helper for the per-event hot path; UPSERTs the row's `process` and `last_activity` without touching `state`:

```sql
INSERT INTO sessions (session_id, project_id, platform, state, process, started_at, last_activity)
VALUES (?, ?, ?, 'needs_input', NULLIF(?, ''), ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
    last_activity = excluded.last_activity,
    process       = COALESCE(NULLIF(excluded.process, ''), sessions.process);
```

In the same transaction, run collision invalidation (only when the incoming `process` is non-empty):

```sql
UPDATE sessions
SET state = 'ended', last_activity = ?
WHERE process = ?              -- the new non-empty process
  AND session_id != ?          -- not this session
  AND state != 'ended';
```

The `needs_input` initial state matches existing `InitSession` semantics; the UPDATE branch never overwrites `state` so lifecycle transitions (`working` / `idle` / `ended`) stay owned by `BindSessionToTask`, `IdleSession`, `EndSession`.

Existing `BindSessionToTask` / `StartChatSession` / `SetProcess` / `BackfillProcess` keep their current semantics. `TouchSession` is the per-event hot path; the others handle state transitions and remain.

Add `ReapDeadTmuxPanes(projectID int64) error`:

```go
// 1. exec: tmux list-panes -a -F '#{pane_id}'
// 2. If tmux is not running or exits non-zero, no-op cleanly (do not error out the hook).
// 3. Collect alive %-panes into a set.
// 4. UPDATE sessions SET state='ended', last_activity=?
//    WHERE state != 'ended'
//      AND process GLOB '%[0-9]*'  -- tmux pane format only
//      AND process NOT IN (<alive set>)
//      AND project_id = ?;
```

Tmux-specific by name and scope. Non-tmux `process` values (e.g. `pid:N`) are untouched; their lifecycle is governed by their own platform's mechanisms (lifecycle hook on SessionEnd; future per-platform reaper if needed).

**`cmd/endless-hook/claude.go`** — at the top of `ProcessHook` after payload parsing and before the event switch:

```go
if err := monitor.TouchSession(payload.SessionID, "claude", os.Getenv("TMUX_PANE"), projectID); err != nil {
    return fmt.Errorf("touching session: %w", err)
}
```

This runs for every event. Inside the event switch:

- **SessionStart**: remove the `writeClaudeCompanion` call (line 166). Keep `InitSession` for now (its `state='needs_input'` matches the TouchSession default; consider folding in a follow-up). Add `ReapDeadTmuxPanes(projectID)` (unconditional — no throttle). Keep the spawn-flow auto-bind and worktree-adoption logic.
- **UserPromptSubmit**: remove `BackfillProcess` (superseded by TouchSession) and `writeClaudeCompanion` (line 203).
- **PostToolUse**: remove the three `writeClaudeCompanion` refresh calls (lines 636, 655, 676).
- **SessionEnd**: remove `RemoveCompanion` (line 232). `EndSession` continues to set `state='ended'`.
- **PreToolUse / ExitPlanMode / Stop / PreCompact**: no code change beyond the top-of-handler `TouchSession` already added.

Delete `writeClaudeCompanion` (lines 1002–1041).

### Reader side (Python, via Go shim)

Per [memory: No Python SQLite reads in new code], the new readers must not extend `db.query` — DB access moves to Go (E-894). Add one Go CLI shim that emits JSON; Python parses.

**New Go subcommand** `endless session list-live` — JSON array, one element per live session: `{session_id, endless_session_id, project_id, platform, state, active_task_id, process, pane_id (process when %-form, else null), started_at, last_activity, summary}`. No `cwd`, no `pid`, no `worktree_path` — derived in Python.

**`src/endless/session_cmd.py`**:
- Delete `_pid_alive` (lines 984–997) and `_read_live_companions` (lines 1000–1026).
- Add `_live_sessions(project_root: Path) -> list[dict]` that shells out to `endless session list-live --project-root <root>` and parses JSON.
- Add `_tmux_pane_cwd(pane_id: str) -> str | None` using `tmux display-message -p -t <pane> '#{pane_current_path}'`; returns `None` on failure or for non-tmux `process` values. Naming makes the platform assumption explicit; future non-tmux harnesses add their own helper.
- Migrate `show_history` / `session_cd_resolve` / `session_show_resolve` / `session_use_resolve` to consume `_live_sessions` dicts (drop `_path`, derive `cwd` on demand, derive `worktree_path` from `active_task_id`).

**`src/endless/task_cmd.py`**:
- `_current_endless_session_id` (line 1997) → filter `_live_sessions` by `pane_id == TMUX_PANE` and return the eid.
- `_list_sibling_claude_session_eids` (line 2020) → filter `_live_sessions` by `pane_id in sibling_panes`.
- `_check_task_ownership` (line 2159) and `task_release_resolve` (line 2459) → `_live_sessions` keyed by `endless_session_id`; "is the owner alive" becomes "is the eid in the live set."

### Critical files

- `internal/monitor/companion.go`
- `internal/monitor/session.go`
- `internal/monitor/worktree.go` (new, or absorb `WorktreePathForTask` into an existing monitor file)
- `cmd/endless-hook/claude.go`
- `cmd/endless/session.go` (or wherever `session` subcommands live — add `list-live`)
- `src/endless/session_cmd.py`
- `src/endless/task_cmd.py`

## Implementation notes (not architecture)

- **Migration approach**: single-PR hard cutover. Per-event refresh makes the upgrade window self-heal within one tool call (any session whose row got dropped or never had `process` set is repaired on its next hook event).
- **Out of scope for this task**: archived sessions (those parked in a tmux session literally named `archive`). Their Claude processes are alive, so both companion-file and DB-only schemes treat them as live — no regression introduced here. A separate task will design how Endless represents archived sessions (`docs/private/endless-tmux-archive-research.md`).

## Follow-ups to file as part of implementation

- **Archive subsystem task** — Mike confirmed he never filed this. Inputs: `docs/private/endless-tmux-archive-research.md`. Default `--type task`, status `needs_plan`. Not a `--cleans-up` of E-1426 (adjacent concern surfaced during planning, not introduced by E-1426).
- **Cleans-up flips** at land time per task brief: E-1408, E-1419, E-1422 → `assumed` once the verify-side E2E tests in this PR demonstrate each symptom is gone; `confirmed` only if Mike independently reproduces the original failure on this branch and sees it pass.

## Verification

**Go unit tests** (new + extend):
- `TouchSession`: INSERT path creates a `needs_input` row with `process` set when non-empty; UPDATE path overwrites `last_activity` and only overwrites `process` when the new value is non-empty; `state` is preserved across calls; the `platform` parameter is honored.
- Collision invalidation: `TouchSession(sessionA, "claude", "%5", ...)` followed by `TouchSession(sessionB, "claude", "%5", ...)` leaves `sessionA.state = 'ended'`.
- `ReapDeadTmuxPanes`: with a stubbed `tmux list-panes` returning `{%5, %12}`, a row with `process='%99'` is marked `ended`; rows with `process='%5'` are untouched; rows with `process='pid:1234'` are untouched (non-tmux format); rows already `state='ended'` are not rewritten; tmux-not-running is a clean no-op.
- E-1408 regression: ProcessHook with `TMUX_PANE=""` at SessionStart followed by a PreToolUse with `TMUX_PANE="%5"` leaves the row with `process='%5'`.
- E-1419 regression: ProcessHook from a worktree cwd writes to the single shared DB (sandbox if `worktree_sandbox=true`); no `.endless/sessions/` directory is created anywhere.

**Python tests**:
- `_live_sessions` parses the `endless session list-live` JSON and returns the expected shape; handles empty result, subprocess error, malformed JSON.
- `_current_endless_session_id`: pane-match → returns eid; no-match → `None`; multiple matches → most-recent `last_activity` wins (mirroring `GetLiveSessionByProcess`).
- `_list_sibling_claude_session_eids`: filters by sibling-pane set; excludes `state='ended'`.
- `task_release_resolve` stale-binding: a `state='working'` row for a binder whose pane has been reaped resolves as not-live; auto-clear fires.

**End-to-end** in a scratch worktree per [memory: Run my own verification]:
- Start a fresh Claude session in a tmux pane. `endless session list` shows it once. `endless task id` resolves correctly.
- Kill the pane (close window). Trigger any hook event from a sibling pane (or wait for the next SessionStart anywhere). `endless session list` no longer lists the killed pane's session. Verify `state='ended'` in the DB.
- Confirm `ls .endless/sessions/` shows nothing (directory may not exist).
- Start a Claude session with `TMUX_PANE` artificially unset (`env -u TMUX_PANE claude` inside a tmux pane that has it). Verify the first PreToolUse repairs `process` to the real pane id (E-1408 fix).
- Start a Claude session from a worktree cwd. Verify the DB row exists and CLI resolution from that worktree cwd succeeds (E-1419 fix).

## Plan hygiene

After ExitPlanMode: claim the task (`endless task claim 1426`), copy this plan to `.endless/plans/E-1426.md`, attach via `endless task update 1426 --text .endless/plans/E-1426.md`, flip status `needs_plan` → `ready` per [memory: Plan attach promotes to ready], and commit the plan file per [memory: Commit plan files you create]. Then file the archive follow-up task with the literal returned ID per [memory: Use returned task ID literally].
