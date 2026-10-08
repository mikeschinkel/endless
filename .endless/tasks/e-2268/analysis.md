Today an incident row (`errors` table, internal/faults) carries project_id, code, source, fingerprint and summary, and the detail log carries Fields. Nothing names the task or session that was active when the fault was raised.

Faults raised inside a session (hooks, CLI commands run by an agent) can learn both cheaply: the session from ENDLESS_SESSION_ID / the hook payload, the task from that session's sessions.task_id, or from the worktree the command ran in. Faults raised by background jobs have no session; when a job's fault is about a specific task (main-sync naming a task branch, the reaper naming a worktree) the job knows that task and can pass it explicitly.

Shape to settle in the plan: nullable task_id and session_id on the incident (first occurrence) and on each detail line (every occurrence, since one fingerprint can be raised by several sessions); `faults.Fault` gains explicit TaskID/SessionID that override the ambient ones; `endless errors list/show` render them.
