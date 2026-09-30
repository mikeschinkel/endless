release_item in src/endless/task_cmd.py emits the task.released event to clear the DB binding but never touches <worktree>/.endless/worktree.lock.

The lock file is written at claim time with {pid, session_id, claimed_at}.

Hit during E-1281's land attempt 2026-05-14: release ran cleanly, land kept refusing because the originally-claiming Claude session (PID from claim) was still alive.

Workaround was 'rm .endless/worktree.lock' before retry.
