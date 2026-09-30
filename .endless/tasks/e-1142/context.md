After E-1126's complete→confirm rename, the hook's PostToolUse handler clears sessions.active_task_id when Claude runs 'endless task confirm <id>' as a Bash tool call. But Python's complete_item (src/endless/task_cmd.py) does the underlying DB writes and never touches the session row.

Surfaced during E-1126 verification.
