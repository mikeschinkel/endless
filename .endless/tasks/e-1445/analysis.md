Change the trigger from 'no worktree exists' to 'task has been claimed (status=in_progress) OR has sessions.active_task_id binding'.

Until claimed, --text sets the DB text and writes the plan into the main checkout's .endless/plans/ for later claim to copy in.
