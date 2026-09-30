So 'endless task start E-1027' typed in a Bash tool is silently a no-op for the session-state side effect: StartWorkSession never fires, sessions.active_task_id stays NULL.
