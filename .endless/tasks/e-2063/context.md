Claude Code mints a NEW session UUID on a clear while the Endless session, the tmux window and the task are all unchanged (verified:

CLAUDE_CODE_SESSION_ID 738849b7 -> 8735f6a4, same CLAUDE_PID 18201).

sessions.session_id stores that UUID, so the intended invariant — one Endless session per task, forever — breaks the moment a user clears.
