PostToolUse injection from the same binary arrives fine.

The two differ only in JSON shape: PreToolUse/PostToolUse nest under hookSpecificOutput, while UserPromptSubmit/SessionStart emit a bare top-level additionalContext that the harness appears to parse and discard.

Confirmed live: notice 758 for session 1048 was rendered, logged and marked delivered at 10:42:09Z and never arrived, and the per-turn active-task line has never appeared in that session at all.

Silently disables the guide pointer, task-list context, active-task line, change notices, message banner and report-channel rule.
