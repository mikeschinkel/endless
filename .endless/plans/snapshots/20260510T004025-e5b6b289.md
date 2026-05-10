# E-1203 — Refuse `endless task start` when another live session has the task active

## Context

E-971's worktree-level lock (`<worktree>/.endless/worktree.lock`, claimed via `O_EXCL` per E-1084) prevents two sessions sharing the **same** worktree. It does NOT prevent two sessions claiming the **same task** in two **different** worktrees: each session would auto-create or pick a different worktree, both bind the same task ID, and both produce divergent branches that conflict at merge time.

Add a task-level check at `endless task start <id>` so this case is refused upfront with a clear message naming the conflicting session.

## Design

### Where the check goes

`src/endless/task_cmd.py` `start_item()` at line 1760. The new check slots in **after** the task-existence check (line 1768) and **before** the `_resolve_project` / `emit_event` calls (line 1774):

```python
    if not row:
        raise click.ClickException(f"No task found with id {item_id}")

    _refuse_if_task_in_use(item_id)   # ← NEW

    _, proj_name = _resolve_project(None)
    emit_event(...)
```

This is correct ordering: we already know the task exists, but we have not yet emitted any state-change event or created any worktree. A refusal here is a clean no-op.

### Why the check is needed even though the DB query naturally excludes the current session

The current session has not yet been bound to this task at the moment `start_item()` runs — the session→task binding happens in Go (`internal/monitor/session.go` `StartWorkSession`) **after** Python emits `task.status_changed`. So a fresh first-time call cannot match itself in the DB.

But a **re-invocation** of `task start E-XXX` from the *same* session that already owns the task **would** match itself in the `WHERE active_task_id = ?` query. We need to exclude the current session by its endless_session_id (int PK) so re-runs do not falsely refuse.

### Helper: refuse logic

New private helper in `src/endless/task_cmd.py`. Reuses existing `_read_live_companions()` and `_project_root_for_cwd()` from `session_cmd.py` (both private but consistent with how `task_cmd.py` already reaches into peer modules — see line 1792 importing from `worktree_cmd`).

```python
def _current_endless_session_id() -> int | None:
    """Best-effort lookup of the current Endless session id (int PK).

    Returns None if we cannot determine it; callers should treat that as
    "no exclusion possible" — safe because the DB query result is then
    treated as fully external.
    """
    import os
    env_id = os.environ.get("ENDLESS_SESSION_ID")
    if env_id and env_id.isdigit():
        return int(env_id)
    pane = os.environ.get("TMUX_PANE")
    if not pane:
        return None
    from endless.session_cmd import _read_live_companions, _project_root_for_cwd
    project_root = _project_root_for_cwd()
    live = _read_live_companions(project_root / ".endless" / "sessions")
    for c in live:
        if c.get("pane_id") == pane:
            eid = c.get("endless_session_id")
            if isinstance(eid, int):
                return eid
    return None


def _refuse_if_task_in_use(item_id: int) -> None:
    """Refuse `task start` if another live session has this task active.

    Stale (dead-PID) sessions are silently ignored — `_read_live_companions`
    prunes them as a side effect.
    """
    rows = db.query(
        "SELECT id AS eid FROM sessions "
        "WHERE active_task_id = ? AND state != 'ended'",
        (item_id,),
    )
    if not rows:
        return

    current_eid = _current_endless_session_id()
    candidate_eids = [r["eid"] for r in rows if r["eid"] != current_eid]
    if not candidate_eids:
        return

    from endless.session_cmd import _read_live_companions, _project_root_for_cwd
    project_root = _project_root_for_cwd()
    live = _read_live_companions(project_root / ".endless" / "sessions")
    live_by_eid = {
        c["endless_session_id"]: c
        for c in live
        if isinstance(c.get("endless_session_id"), int)
    }

    for eid in candidate_eids:
        comp = live_by_eid.get(eid)
        if comp is None:
            # DB row says active but no live companion file → process gone.
            # Treat as stale and skip.
            continue
        pane = comp.get("pane_id", "?")
        raise click.ClickException(
            f"E-{item_id} is already active in session {eid} "
            f"(pid {comp['pid']}, tmux pane {pane}).\n"
            "Switch to that session or have it release the task first."
        )
```

### Why `_read_live_companions` is the right liveness signal

It already encapsulates the PID-liveness sweep we need:
- Reads `<project_root>/.endless/sessions/claude-*.json` companion files (`session_cmd.py:1000`).
- Calls `_pid_alive(pid)` on each (`session_cmd.py:984`, uses `os.kill(pid, 0)` with `ProcessLookupError` / `PermissionError` handling — exactly the `kill -0` semantics the spec requests).
- Side-effect: unlinks stale companion files (lazy cleanup E-989 promised).

The `sessions` DB table has no `pid` column — PIDs only live in companion files. So we must cross-reference companion files against DB rows; the helper above does this in one pass.

### Why we use `WHERE state != 'ended'` rather than `WHERE state = 'working'`

Valid `sessions.state` values: `'working'`, `'idle'`, `'needs_input'`, `'ended'` (`db.py` line 300, app-validated). An idle or input-blocked session still owns its task — only `'ended'` means released. Spec also specifies this filter.

## Critical files to modify

- `src/endless/task_cmd.py` — add `_current_endless_session_id()` and `_refuse_if_task_in_use()` helpers; call the latter in `start_item()` between line 1768 and line 1774.

No other files change. No DB schema change. No Go changes.

## Reused functions

- `db.query` — `src/endless/db.py:575` (already used throughout `task_cmd.py`)
- `_read_live_companions` — `src/endless/session_cmd.py:1000` (PID-pruning companion reader)
- `_project_root_for_cwd` — `src/endless/session_cmd.py:1029`
- `_pid_alive` — `src/endless/session_cmd.py:984` (called transitively via `_read_live_companions`)
- `click.ClickException` — error pattern matching `start_item`'s existing `raise click.ClickException(f"No task found with id {item_id}")` at line 1770

## Edge case handling

| Case | Behavior |
|---|---|
| No competing sessions | Proceeds (DB query returns empty) |
| Current session re-runs `task start E-XXX` for a task it already owns | Proceeds (own eid filtered out via `current_eid` exclusion) |
| Competing DB row exists but PID is dead | Proceeds (no live companion → `comp is None` → skipped; stale companion file is also unlinked as a side effect of `_read_live_companions`) |
| `TMUX_PANE` and `ENDLESS_SESSION_ID` both unset | `current_eid = None`; no exclusion; competing row triggers refusal — acceptable false-positive in non-interactive contexts (rare) |
| PID exists but recycled to different process | `kill -0` returns success → treated as live → refused. Cost is low: spec accepts this as an acceptable false-positive |
| Task is in terminal status (`verify`, `confirmed`, etc.) | Existing `start_item` flow already handles this further down; check above sits before status-transition logic and is harmless |

## Verification

Manual end-to-end (two-terminal smoke test):

1. **Single session:** in worktree A, `endless task start E-XXX` → succeeds, prints worktree path.
2. **Concurrent double-start (the bug we're fixing):**
   - Session A in pane %1: `endless task start E-XXX` → succeeds.
   - Session B in pane %2: `endless task start E-XXX` → **refused** with message naming session A's eid, pid, and pane.
3. **Stale sweep:** Kill session A's tmux pane (or `kill -9` its PID) without running `endless task release`. From session C: `endless task start E-XXX` → **succeeds** (stale companion file gone after sweep). Verify `.endless/sessions/claude-<A-uuid>.json` is unlinked.
4. **Independent tasks:** Two live sessions, A starts E-XXX, B starts E-YYY → both succeed.
5. **Self re-run:** From session A (already owning E-XXX), run `endless task start E-XXX` again → succeeds (current_eid exclusion).

Run inside an E-1203 worktree (use `PYTHONPATH=<worktree>/src endless task start ...` per Mike's "no `just install` from worktree" rule).

## Constraints honored

- Companion file (not "sidecar") terminology.
- snake_case for code/DB; kebab-case for any user-facing string (none added here beyond the error message, which uses natural prose).
- One worktree per task: do this work in `.endless/worktrees/e-1203` (SessionStart will provision).
- Ship via `endless worktree land 1203`; mark task `verify` (not `completed`) when done.
- Do not modify SessionStart, UserPromptSubmit, PreToolUse hooks, or worktree land/drop logic.
- Memory note about "no Python SQLite reads in new code" (E-894): the spec explicitly directs use of the existing `db.query` helper here, and the reads are minimal and scoped to a check that lives alongside the existing `db.query` task lookup in the same function. Honoring spec over the general rule for this localized case.
