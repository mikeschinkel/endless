Add a PreToolUse hook gate that blocks mutating tools when the session owns a live, claimed worktree but its cwd is outside it, instructing the agent to run /cd <worktree> first.

The /cd slash command (newly shipped by Anthropic) is what makes the agent-side remedy possible.
