tasks_notify_sessions (E-1917) fires on a task UPDATE and inserts one session_notices row per involved session, which the UserPromptSubmit drain renders as an FYI line.

The decisions table carries only decisions_updated_at — no fan-out.

So a session files a decision, the user accepts or rejects it minutes later, and the session never learns; it keeps answering as though the call is still open.

Seen directly: the session that filed this had two decisions adjudicated mid-conversation, was told neither, and went on to state one of their statuses from memory, wrongly.
