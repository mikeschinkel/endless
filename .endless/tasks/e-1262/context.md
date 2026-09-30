Today `task confirm` / `task assume` / `task decline` clear `sessions.active_task_id` to NULL — the session becomes idle and loses the link to the task it was just working on.

Surfaced 2026-05-11: the status bar can no longer show what a session was working on after its task lands, even though the user often wants that as context until they explicitly move on.
