After 'endless worktree land' or a terminal-status transition (assumed/confirmed/obsolete/declined), the session->task binding in sessions.active_task_id can outlive the landed worktree.

Result: 'endless-tmux active-id' (and anything else reading the binding) keeps reporting the now-stale task.

Reproduced 2026-05-13: after E-1298 landed via 'just land', the session was still bound to E-1298, and the next 'just land' derived E-1298 and errored 'No endless-managed worktree for E-1298'.\n\nDesign: do NOT auto-clear (the user may want to keep the binding for follow-up work).
