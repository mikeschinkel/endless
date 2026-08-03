Add an append-only, machine-local diagnostic log so incidents in user-machine
state (session bindings first; later worktree locks, sandbox routing, hook
skips) are traceable after the fact. This is NOT the shareable ledger and NOT a
WAL: it is purely observational, machine-local, never replayed into the DB, and
never shared. Motivated by the parent research task: a session's active_task_id
was silently rebound with no trail to inspect.

## Location & format
- File: ~/.config/endless/log/user-machine.jsonl, XDG_CONFIG_HOME-routed so
  worktree sandboxes get their own. Location may move later -- keep it behind a
  single resolver so a future move is one change.
- One JSON object per line, with a top-level `kind` discriminator so the file
  can carry more than one machine-local concern over time. First kind:
  "session".
- Append-only; rotate consistent with whatever ~/.config/endless/log/ already
  does.
- Best-effort: a logging failure must NEVER block a hook or a session write.

## First kind: session state transitions (ALL transitions)
Emit a line at every sessions-table write site:
- internal/monitor/session.go: BindSessionToTask, IdleSession, EndSession, and
  the paneless-dedup UPDATE (the `SET state='ended'` for stale paneless rows).
- internal/events/executor.go: execTaskClaimed, and the release path that NULLs
  active_task_id/active_epic_id.
Suggested fields per line: ts, session GUID, short_id,
old_state -> new_state, old_active_task_id -> new_active_task_id,
reason (cwd-bind | spawn-bind | claim-event | idle | end | dedup | release),
cwd, tmux_pane, caller.

## Why these sites
active_task_id is a single pointer with no history, and sessions are
intentionally not journaled (machine-local by design -- see parent outcome).
This log restores diagnosability without changing that design. Had it existed,
the incident would have been one line:
  reason=cwd-bind, old_active_task_id=1835, new_active_task_id=1832,
  cwd=.../.endless/worktrees/e-1832

## Relationship to the behavior fix
Sibling of E-1856 (which stops the bad rebind and adds the live-sibling
refusal). This task is the diagnostic aid; the two are independent and can land
in either order.
