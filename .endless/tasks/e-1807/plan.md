# Auto-reap a dead owning tmux pane in the spawn/claim ownership guard

## Problem

`endless task spawn`/`claim` refuses a task with:

    Error: E-NNNN is already active in session <eid> (tmux pane %NNN).
    Switch to that session or have it release the task first.

…even when pane `%NNN` no longer exists. This happens when a spawn dies before
firing SessionEnd (CPU spike, crash, window closed) and no later SessionStart
runs the reaper: the `sessions` row stays `state != 'ended'` with a `process`
pointing at a now-dead pane, so the guard reads a ghost as a live owner.

Root cause: `_check_task_ownership` (`src/endless/task_cmd.py`) decides "live
owner" from `_live_sessions` (`src/endless/session_cmd.py`), which derives
liveness purely from DB `state != 'ended'` — it never checks whether the tmux
pane is still present. `monitor.ReapDeadTmuxPanes` already exists and is designed
to be called opportunistically; the guard just never consults it.

## Deliverable

The spawn/claim ownership guard self-heals: a ghost owner (non-ended session row
whose tmux pane is gone) is reaped to `ended` before the guard evaluates
ownership, so the task reads as free and the spawn proceeds. No new user step.

## Implementation

1. Expose the reaper to the Python layer as a quiet Go subcommand. Add
   `reap-dead-panes --project-root <path>` to `internal/sessionquerycmd`
   (sibling of `list-live`), wrapping `monitor.ReapDeadTmuxPanes(projectID)`.
   It must NOT carry `tmux reset`'s `$TMUX` hard-fail guard — the reaper already
   no-ops when tmux is unavailable (returns nil), which is the correct behavior
   for an internal opportunistic call. Emit nothing on success (or a JSON count);
   nonzero exit only on a real DB error.

2. Call it before the ownership query. In `_check_task_ownership` (or the
   claim/spawn entry immediately before it), invoke the new subcommand for the
   task's project via subprocess — mirroring how `_live_sessions` already shells
   to `endless-go session-query list-live`. Best-effort: on any subprocess error
   fall through to the existing behavior (never block a spawn on a reaper
   failure). The existing `WHERE active_task_id = ? AND state != 'ended'` query
   then naturally excludes the just-reaped ghost.

3. Keep the "already active" error for the genuinely-live case only. Now that a
   dead-pane owner is reaped first, the error path is reached only for a real
   live collision. Add the manual escape hatch to its text as a fallback:
   mention that `endless-go tmux reset` clears stale session rows if the owning
   pane is actually gone. (Secondary; the auto-reap is the primary fix.)

## Verification

Deliver one per-task verify script, `tests/tasks/e-1807-verify.sh`, as the sole
command the user runs (`esu && ./tests/tasks/e-1807-verify.sh`). Model it on
`tests/tasks/e-1802-verify.sh`: a fail-fast unit-test gate first, then the
end-to-end checks against the worktree's sandbox DB.

- Fail-fast gate (top of the script): `go test ./internal/sessionquerycmd/ ...`
  plus the Python ownership test — abort the run on any failure before the E2E
  section.
- E2E: seed a non-ended session on a dead `%`-pane in the sandbox, run the new
  `reap-dead-panes` path, and assert the row flips to `ended` with `process`
  NULLed — then assert a spawn/claim ownership check on that task now treats it
  as free (no "already active" collision).

### Tests the script gates on

- `internal/sessionquerycmd`: a `reap-dead-panes` binary test seeding a
  non-ended session on a dead `%`-pane and asserting the row flips to `ended`
  with `process` NULLed (mirror the existing `ReapDeadTmuxPanes` reap tests).
- Python: a `_check_task_ownership` test where the sole owner's pane is absent
  from the live set — assert the task is treated as free (returns False), not a
  raised collision. Reuse the existing spawn/claim test fixtures.

## Out of scope

Broader session-end lifecycle (idle-timeout closure) is E-1642; this task only
closes the spawn/claim ghost-owner gap.
