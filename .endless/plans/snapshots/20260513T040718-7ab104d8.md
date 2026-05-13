# E-1291 — Auto-bind session to task by cwd-in-worktree on SessionStart

## Context

After E-1274 landed, the spawn flow does the full claim (status flip + worktree creation) before launching Claude, and `SessionStart`'s auto-bind keys off `@endless_spawned_by` to record the session→task binding. Spawned sessions land in a worktree fully claimed.

For the non-spawn case — a user who manually cd's into an existing worktree and runs `claude` directly (e.g., `cd .endless/worktrees/e-NNN && claude`, `eswt E-NNN`, or any equivalent) — `handleWorktreeAdoption` claims the filesystem lock but no session→task binding is set. The user has to run `endless task claim E-NNN` to get the binding.

In Mike's standard workflow this case is uncommon (he runs `cd /path/to/project && claude` in the main checkout, brainstorms, then `endless task add` + `endless task claim`; the binding gets set via the post-bash `endless task claim` detector at `cmd/endless-hook/claude.go:597`). E-1291 closes the small remaining gap for users who do go straight into a worktree — bind from the companion file's `task_id` so they don't have to also run `claim`.

## Recorded design decisions

- **Bind only, not auto-claim.** Set `sessions.active_task_id`; do not flip `tasks.status`. Status mutations should not be a side effect of a hook event the user didn't directly invoke.
- **Skip Agent-tool subagents.** `SessionStart` fires for subagents too, with their own distinct `session_id` but the parent's `cwd`. Auto-binding them would create phantom co-owners on the task. Add `AgentID` (and `AgentType`, `Source`) to `claudePayload`; gate the new block on `AgentID == ""`.
- **Don't branch on the `Source` field.** `resume`/`clear`/`compact` cases re-fire `SessionStart` but binding is idempotent (`active_task_id` ends up at the same value), so a single cwd check suffices regardless of source.
- **Don't tighten the spawn-flow path's missing subagent guard here.** Filed as separate follow-up (see below).

## Approach

Python-free change. Only touches `cmd/endless-hook/claude.go` and uses existing Go helpers in `internal/monitor`.

### 1. Extend `claudePayload`

```go
type claudePayload struct {
    // ... existing fields ...
    Source    string `json:"source,omitempty"`     // "startup" | "resume" | "clear" | "compact"
    AgentID   string `json:"agent_id,omitempty"`   // present for Agent-tool subagents
    AgentType string `json:"agent_type,omitempty"` // present for Agent-tool subagents
}
```

Only `AgentID` is consulted in this change; the other two are added for future use and so we don't have to roll the struct again on the first follow-up that needs them.

### 2. Add a cwd-bind block in the `SessionStart` arm

In `cmd/endless-hook/claude.go`, inside `case "SessionStart"`, after `handleWorktreeAdoption(...)` succeeds and before `handleTaskContextInjection(...)`:

```go
// Cwd-based auto-bind (E-1291): if cwd is inside an endless worktree
// and the spawn-marker path didn't already bind, set the session's
// active_task_id from the worktree companion. Skipped for Agent-tool
// subagents (they share parent cwd but represent tool use, not user
// claim intent). Bind only — does not flip task status.
if payload.AgentID == "" && tmuxSpawnedBy() == "" {
    if wtRoot, err := monitor.FindWorktreeRoot(payload.CWD, projectRoot); err == nil && wtRoot != "" {
        if companion, err := monitor.ReadWorktreeCompanion(wtRoot); err == nil && companion != nil {
            if taskID := parseCanonicalTaskID(companion.TaskID); taskID > 0 {
                monitor.BindSessionToTask(payload.SessionID, projectID, taskID)
            }
        }
    }
}
```

`parseCanonicalTaskID` is a small helper that strips the `E-` prefix and parses the integer. (If a similar helper already exists in `internal/monitor` or `internal/events`, reuse instead of adding.)

`projectRoot` is already available in the surrounding code (used by `handleWorktreeAdoption` at claude.go:1172).

### 3. No documentation update needed

The orchestration guide doesn't currently advertise the manual `cd <worktree> && claude` pattern as a flow — and per Mike, it's uncommon. This change is a quiet quality-of-life fix; no user-visible doc surface needs updating.

## Critical files

- `cmd/endless-hook/claude.go` — add `Source` / `AgentID` / `AgentType` to `claudePayload` (struct around line 42-50); add the cwd-bind block in the `SessionStart` switch arm (around line 162, after `handleWorktreeAdoption`)

## Reused / not new

- `monitor.FindWorktreeRoot(cwd, projectRoot)` — `internal/monitor/worktree_lock.go:220`
- `monitor.ReadWorktreeCompanion(worktreePath)` — `internal/monitor/worktree_lock.go:53`
- `monitor.BindSessionToTask(sessionID, projectID, taskID)` — `internal/monitor/session.go` (added in E-1274)

## Verification

Run from the worktree (`.endless/worktrees/e-1291`):

1. **Build:** `just build` from the main checkout. Confirm clean `go build`.
2. **Existing tests:** `just test` (Python) + `go test ./internal/monitor/... ./cmd/endless-hook/...`.
3. **New unit tests** in `cmd/endless-hook/` covering:
   - Subagent payload (`AgentID` set) + cwd in a worktree → no bind.
   - Cwd not in any worktree (main checkout) → no bind.
   - Cwd in a worktree, no `@endless_spawned_by`, no `AgentID` → binds; verify `sessions.active_task_id` reflects the companion's task.
   - Cwd in a worktree, `@endless_spawned_by` set → spawn-flow path fires; new block is short-circuited (no double-write); session ends up bound (via spawn-flow path), task status unchanged from the cwd-bind path's perspective.
4. **Smoke test:**
   - Claim a fresh task in one tmux pane: `endless task claim E-XYZ` (creates worktree).
   - In a different terminal, `cd <main>/.endless/worktrees/e-XYZ`, then `claude`.
   - Confirm `endless sql "SELECT session_id, active_task_id FROM sessions ORDER BY id DESC LIMIT 3"` shows the new session bound to E-XYZ.
   - Confirm `endless task show E-XYZ` still reports the same status it had before (no flip).
5. **Regression check:** in the standard workflow (`cd /path/to/project && claude`), confirm `SessionStart` does not bind (cwd has no companion, no auto-bind path fires). Behavior unchanged.

## Follow-up

Filed after implementation lands:

```
endless task add "Skip subagents in spawn-flow SessionStart auto-bind" --cleans-up E-1291 \
  --description "The @endless_spawned_by auto-bind block in cmd/endless-hook/claude.go's SessionStart arm doesn't currently guard against Agent-tool subagents — a subagent firing SessionStart inside a spawn-marked tmux window will bind its own session_id to the spawned task, creating a phantom co-owner. Same latent issue E-1291 addressed for the cwd path; analogous fix on the spawn-flow path. Skip when payload.AgentID != ''."
```
