Confirmed in main: session 845 (E-1699, pane %308) has active_task_id NULL while sibling spawned sessions 844->1695 and 843->1698 are correctly bound, so the bind mechanism works and 845 is the outlier.

Not sandbox-vs-main; everything's in main.

No task.claimed event exists (spawn only flips status; binding is hook-side via BindSessionToTask).

active_task_id is cleared to NULL by StartChatSession (session.go) on a chat takeover - leading candidate.
