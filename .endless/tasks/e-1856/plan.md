Two defects in the SessionStart auto-bind path let a resume silently repoint a
session's active_task_id and proceed even when another live session owns the
target worktree. This is how a task's Claude session becomes unreachable via
`session goto`/`session resume`.

## Repro / incident (confirmed end-state)
- The Claude session that ran E-1835's worktree is endless id 997, GUID
  17805d28-eb95-4b65-b3a1-9c0b0ca6b234, state=ended, transcript still at
  .../-endless-worktrees-e-1835/17805d28-...jsonl.
- Yet its active_task_id now reads 1832 (the README task). So
  `session goto 1835` / `session resume 1835` find no session for task 1835 and
  emit the confusing "no resumable Claude session found for task E-1835" /
  "no task E-1835 with a resumable session, and no session id 1835".

## Root cause (forensics under the parent research task)
- `session resume` is READ-ONLY: resume_session (src/endless/session_cmd.py) and
  internal/monitor/resume.go only resolve a target and execvp
  `claude --resume <uuid>`. They never write active_task_id. The row was NOT
  deleted.
- The active_task_id write is done by the SessionStart hook:
  maybeCwdBind -> autoBindFromCwd (internal/hookcmd/claude.go:1079-1103) derives
  the task id from the cwd worktree DIRECTORY NAME (resolveCwdTaskID,
  `.endless/worktrees/e-NNN`) and calls monitor.BindSessionToTask -- a direct,
  non-ledgered `UPDATE sessions SET active_task_id=?`
  (internal/monitor/session.go:53). So whenever the resumed `claude --resume`
  process's cwd is a worktree dir for a task OTHER than the one being resumed,
  the session is silently rebound to that other task.
- There is NO `task.claimed` event for the involved task ids anywhere in the
  .endless/db-ledger, confirming the bind came from this direct path, not a
  ledgered claim.
- active_task_id is a single mutable pointer with no history, and sessions are
  NOT journaled to the ledger, so the prior binding is unrecoverable from the
  WAL. The only surviving link is sessions.transcript_path.

## Required behavior
1. When a session's target worktree is already owned by a LIVE sibling session
   (worktree lock held by a different, non-stale session), refuse with an
   actionable error instead of binding. handleWorktreeAdoption (claude.go:1258)
   already has a live-owner refusal branch for the LOCK, but the cwd auto-bind
   (maybeCwdBind/autoBindFromCwd) writes active_task_id without consulting it.
   Gate the auto-bind on the same live-owner check.
2. A resume must not repoint active_task_id to a task different from the one
   being resumed. The resume flow should carry the intended task through to the
   relaunched session, and the SessionStart auto-bind must not override it from
   the cwd directory name.

## Out of scope (separate finding, in parent research outcome)
Sessions are not journaled to the ledger at all, so this entire class of
session-state loss is unrecoverable after the fact. Whether to journal session
bindings (or make active_task_id append-only/historical) is an architectural
decision to be filed and decided separately.
