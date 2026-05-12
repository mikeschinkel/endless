# E-1274 — Fix spawn pre-binding session to task without creating worktree

## Context

`endless task spawn E-NNN` opens a new tmux window with Claude and pastes the task's prompt. Today it sets `@endless_task_id=NNN` on the new window before launching Claude. When the spawned Claude starts, its `SessionStart` hook reads `@endless_task_id` and calls `monitor.StartWorkSession()` (cmd/endless-hook/claude.go:144-146), which in one Go function both (a) writes `sessions.active_task_id=NNN` and (b) flips `tasks.status` to `in_progress`. No events are emitted; the state change happens via direct SQL.

By the time the spawned Claude runs `endless task claim E-NNN` (per orchestration boilerplate), the session is already bound and the status is already flipped. `claim_item` calls `_check_task_ownership()` which returns True (self-bound), then short-circuits at `task_cmd.py:2134-2156` — prints "E-NNN is already active in session N" (misleading for a fresh-spawned session that thinks it's claiming for the first time), creates the worktree, returns. The full claim path that emits `task.status_changed` and `task.claimed` events is skipped.

Two parts of the original spec are no longer accurate (E-1229 landed before E-1274 was filed):
- "no worktree" — short-circuit does create the worktree as of E-1229.
- "status still ready/needs_plan" — status is flipped, by `StartWorkSession`'s direct SQL.

What remains to fix:
- The "already active in session N" message reads as an error to the spawned Claude.
- State mutations bypass the event log (no `task.status_changed`, no `task.claimed` event for the spawn-then-claim flow).
- The full claim path's help text ("To work on this task: ...") is skipped.
- `@endless_task_id` is overloaded — it means both "this window has task X in context" *and* "this window was spawned with task X as the intent". Any future code that sets `@endless_task_id` for window-context reasons would unintentionally trigger spawn-flow logic. (Mike's footgun call.)

Mike's chosen direction:
1. Spawn does the full claim (status flip + worktree) *before* launching Claude.
2. Introduce a distinct spawn marker `@endless_spawned_by=<spawner_id>`. The SessionStart hook keys off this — not `@endless_task_id` — to decide spawn-flow vs. non-spawn behavior.
3. SessionStart's spawn-flow path emits `task.claimed` for audit, and does *not* flip status (spawn already did it).
4. Use case 2 (end user starts a Claude session directly, e.g. inside a worktree via `eswt`) is *not* touched in this task — today it doesn't auto-bind from `@endless_task_id` either (no tmux marker is set), so it still works correctly via the user's explicit `endless task claim`. Adding cwd-based auto-bind for use case 2 is filed as a separate follow-up with `--cleans-up E-1274`, coordinated with E-1195 (worktree.lock removal).

## Approach

Mix of Python (spawn pre-claim, claim refactor) and Go (split `StartWorkSession`, change SessionStart's hook key).

### 1. Python: extract shared `_perform_claim_work` helper

In `src/endless/task_cmd.py`. Private helper used by both `claim_item` and `spawn_plan`:

```python
def _perform_claim_work(
    item_id: int,
    title: str,
    current_status: str,
    target_session: int | None,
    proj_name: str,
) -> tuple[Path, bool]:
    """Emit claim events and create the worktree. Returns (wt_path, created).

    Caller has already resolved target_session and validated the
    status/ownership gates. target_session=None means no session binding
    yet (spawn case: Claude hasn't started).
    """
    if current_status != "in_progress":
        emit_event(
            kind="task.status_changed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={"old_status": current_status, "new_status": "in_progress"},
        )
        _emit_field_changes(item_id, title,
                            [("status", current_status, "in_progress")])

    if target_session is not None:
        emit_event(
            kind="task.claimed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={"session_id": target_session},
        )

    return create_task_worktree(item_id, title or "task", _project_root())
```

The done-ish-status gate (`_CLAIM_REQUIRES_FORCE`) and the multi-owner refusal in `_check_task_ownership` stay in their respective callers (they run *before* `_perform_claim_work`).

### 2. Python: refactor `claim_item` to use the helper

Public behavior preserved. The full path delegates to `_perform_claim_work(..., target_session=<resolved>, ...)`. The short-circuit path (`_check_task_ownership` returns True — self-already-bound) stays as the friendly notice + idempotent worktree create. This keeps `claim` safe if the spawned Claude (or a user) runs it after spawn already claimed.

### 3. Python: update `spawn_plan` to do the claim work before tmux window creation

In `spawn_plan` (task_cmd.py:2726+), insert the claim work between prompt validation and `tmux new-window`:

```python
# After prompt/--worktree validation, before window creation:

# Done-ish status gate — mirror claim_item
if not force and current_status in _CLAIM_REQUIRES_FORCE:
    raise click.ClickException(...)

# Refuse if another live session already owns the task.
# Pass current_eid=None so any owner is treated as "other".
_check_task_ownership(item_id, current_eid=None)

# Resolve the spawner's identity for the spawned-by marker
spawner_id = _current_endless_session_id() or f"pid-{os.getpid()}"

# Do the claim work (no session binding yet — Claude not started)
_, proj_name = _resolve_project(None)
wt_path, _ = _perform_claim_work(
    item_id, title, current_status,
    target_session=None,
    proj_name=proj_name,
)

# Default cd_target to the new worktree (existing --worktree still overrides)
if worktree is None:
    cd_target = str(wt_path)
```

Then when creating the tmux window, set the new spawn marker alongside the existing task-id var:

```python
subprocess.run(
    ["tmux", "set", "-w", "-t", window_name,
     "@endless_spawned_by", str(spawner_id)],
    check=True,
)
subprocess.run(
    ["tmux", "set", "-w", "-t", window_name,
     "@endless_task_id", str(item_id)],
    check=True,
)
```

Add a `--force` CLI flag to `spawn` (mirrors `claim --force`).

### 4. Go: split `StartWorkSession`

In `internal/monitor/session.go`, factor today's single function into two:

```go
// BindSessionToTask records the session-task binding only.
// Does NOT change task status — caller (typically via task.claimed
// event executor or spawn's pre-claim flow) is responsible for status.
func BindSessionToTask(sessionID string, projectID int64, taskID int64) error {
    // (current StartWorkSession body, MINUS the "UPDATE tasks SET status" line)
}

// StartWorkSession binds the session AND marks the task in_progress.
// Used by the post-bash `endless task claim` detector (defense-in-depth
// alongside the Python-emitted task.status_changed event).
func StartWorkSession(sessionID string, projectID int64, taskID int64) error {
    if err := BindSessionToTask(sessionID, projectID, taskID); err != nil {
        return err
    }
    return markTaskInProgress(taskID)  // the existing UPDATE-eligible-statuses query
}
```

### 5. Go: narrow SessionStart's auto-bind to `@endless_spawned_by`

In `cmd/endless-hook/claude.go:141-146`, change the keying from `@endless_task_id` to `@endless_spawned_by`. Add a `tmuxSpawnedBy()` helper paralleling the existing `tmuxTaskID()`:

```go
// In the SessionStart switch arm:
if spawnedBy := tmuxSpawnedBy(); spawnedBy != "" {
    if taskID := tmuxTaskID(); taskID > 0 {
        // Spawn flow: status already flipped by spawn's pre-claim;
        // only the binding remains. Use BindSessionToTask, not
        // StartWorkSession, to avoid a redundant status-flip path.
        monitor.BindSessionToTask(payload.SessionID, projectID, taskID)
    }
}
```

When `@endless_spawned_by` is unset (use case 2 — user-started Claude in a worktree), the spawn-flow code does not run. The session is unbound from any task; the user runs `endless task claim` as today, which exercises the full claim path. This preserves use-case 2 behavior unchanged.

The post-bash `endless task claim` detector at claude.go:597 keeps calling `StartWorkSession` (no change there — it remains the defense-in-depth mirror of Python claim_item's events).

### 6. Documentation update — `docs/guide/orchestration.md`

- Line 155 ("Spawn does *not* auto-claim the task..."): change to describe new behavior — spawn flips status and creates the worktree before launching Claude; the spawned session lands in the worktree already claimed; SessionStart records the session binding via `task.claimed` event.
- Lines 176 and 205 (the example prompt's `endless task claim` boilerplate): remove the claim step (or note it is a safe idempotent no-op).
- Add `--force` for spawn alongside `--no-plan` and `--worktree`.

### 7. File follow-up task (during implementation, not plan mode)

After the implementation lands, file:
```
endless task add "SessionStart auto-binds session to task by cwd-in-worktree (use case 2)" \
    --cleans-up E-1274 \
    --description "Today, an end-user `claude` session in a worktree only claims the filesystem lock; the task-session binding is not auto-set. Make SessionStart auto-bind sessions.active_task_id when cwd is inside an endless worktree and the task is unclaimed by a live session. Coordinate with E-1195 (worktree.lock removal) so this is built on the DB-backed binding/lock model, not the legacy lock file."
```

## Critical files

- `src/endless/task_cmd.py:2026-2222` — `claim_item`, `_check_task_ownership` (refactor; extract helper)
- `src/endless/task_cmd.py:2726-2867` — `spawn_plan` (add pre-claim, set `@endless_spawned_by`, add `--force`)
- `src/endless/cli.py` (spawn CLI option registration) — `--force` flag
- `internal/monitor/session.go:23-52` — split `StartWorkSession` into `BindSessionToTask` + `StartWorkSession`
- `cmd/endless-hook/claude.go:141-146, 993-1015` — narrow auto-bind to `@endless_spawned_by`; add `tmuxSpawnedBy()` helper
- `src/endless/worktree_cmd.py:606-658` — `create_task_worktree` (no change; already idempotent)
- `docs/guide/orchestration.md:155, 176, 205` — text updates

## Reused / not new

- `_perform_claim_work` uses existing event kinds (`task.status_changed`, `task.claimed`) and existing executor logic in `internal/events/executor.go`.
- `create_task_worktree` already returns `(path, created)` and is idempotent on existing canonical worktree.
- `_check_task_ownership` already raises with a clear message when a different live session owns the task.

## Verification

Run from the worktree (`.endless/worktrees/e-1274`):

1. **Build:** `just build` from main checkout (the recipe needs to be run from main per project rules; verify both Go and templ compile cleanly).
2. **Tests:** `just test` for Python.
3. **Smoke test — spawn a fresh task:**
   - From a Claude session in main: `endless task add "verification target" --description "scratch"` → returns E-XXXX.
   - `endless task update E-XXXX --prompt /tmp/p.md` (any short prompt).
   - `endless task spawn E-XXXX` from inside tmux. Expect spawn output to include status-flip + worktree-created lines (in addition to the window-spawned line).
   - `endless task show E-XXXX` → status is `in_progress`.
   - `endless worktree for-task E-XXXX` → returns the worktree path.
   - In the spawned tmux window: `pwd` is the worktree directory.
   - In the spawned window: `endless task claim E-XXXX` prints the friendly "already active" notice and exits cleanly (idempotent).
   - Tear down: `endless worktree drop E-XXXX --force` + `endless task update E-XXXX --status obsolete`.
4. **Refusal cases:**
   - Spawn a task in `verify` without `--force` → refused with a `claim_item`-style message.
   - Spawn the same task with `--force` → proceeds.
   - Set up a task owned by a live session in another shell, then `spawn` from your own → refused with "already active in session X" naming the blocker.
5. **Audit log:**
   - `endless sql "SELECT kind, entity_id FROM events WHERE entity_id='XXXX' ORDER BY id"` → shows `task.status_changed` and `task.claimed` for the spawn flow.
6. **Use case 2 sanity check:**
   - `cd "$(endless worktree for-task <some_other_task>)"`, then run `claude` directly (no spawn).
   - Confirm the new Claude session is *not* auto-bound to that task. Running `endless task claim` does the full claim path normally.
