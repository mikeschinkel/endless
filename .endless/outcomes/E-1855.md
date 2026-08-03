# Findings: how a task's Claude session became unreachable

## 1. The session was NOT deleted; its active_task_id was repointed
- E-1835's worktree session: endless id 997, GUID
  17805d28-eb95-4b65-b3a1-9c0b0ca6b234, state=ended, transcript intact at
  ~/.claude/projects/-Users-mikeschinkel-Projects-endless--endless-worktrees-e-1835/17805d28-...jsonl
- Its active_task_id reads 1832 (README task), not 1835. transcript_path still
  points at the e-1835 worktree -- the only surviving link back to 1835.

## 2. session goto / session resume are READ-ONLY -- they did not lose anything
- resume_session (src/endless/session_cmd.py) and internal/monitor/resume.go
  only resolve a target and execvp `claude --resume <uuid>`; no active_task_id
  write. resume.go is all SELECTs.
- The errors were literal outputs of ResolveResumeTarget: no session has
  active_task_id=1835 and no session id=1835, so resumeByTask -> resumeBySessionID
  both miss -> "no task E-1835 with a resumable session, and no session id 1835".

## 3. The repoint came from a SessionStart cwd auto-bind
- maybeCwdBind -> autoBindFromCwd (internal/hookcmd/claude.go:1079-1103) derives
  the task id from the cwd worktree DIRECTORY NAME (resolveCwdTaskID) and calls
  monitor.BindSessionToTask -> direct `UPDATE sessions SET active_task_id=?`
  (internal/monitor/session.go:53). When the relaunched `claude --resume` process
  sat in a worktree dir for a different task (1832), the session was rebound to it.
- active_task_id is a single mutable pointer with no history, so the prior 1835
  binding is simply overwritten.

## 4. Sessions are intentionally machine-local (by design, not a gap)
- The sessions table is deliberately NOT journaled to the .endless/db-ledger:
  session state is user-machine-specific, and the ledger carries only shareable
  project info. The absence of a session WAL is correct, not a defect. (Confirmed
  there is no session.* event kind and no task.claimed event for the involved
  ids anywhere in the ledger.)
- Consequence for diagnosis: there is no built-in trail of session-state changes,
  so an incident like this leaves nothing to inspect after the fact. A SEPARATE
  machine-local diagnostic log (distinct from the shareable ledger) could record
  session mutations to make such incidents easy to trace. Open decision below.

## Follow-ups
- E-1856 (child): fix the two behaviors -- (1) don't repoint active_task_id from
  the cwd worktree name on resume; (2) refuse when a live sibling session owns
  the worktree.
- E-1857 (sibling child): add a machine-local diagnostic log
  (~/.config/endless/log/user-machine.jsonl, per-line `kind`, first kind
  "session") recording ALL session state transitions -- restores diagnosability
  without changing the machine-local design. (Sessions stay out of the shareable
  ledger; this is a separate, observational, machine-local log.)

## Recovery for the current incident
The E-1835 conversation is fully intact. To get back into it:
  cd ~/Projects/endless/.endless/worktrees/e-1835
  claude --resume 17805d28-eb95-4b65-b3a1-9c0b0ca6b234
(Prefer raw claude --resume over `endless session resume` until E-1856 lands,
since the latter would re-trigger the cwd rebind.)
