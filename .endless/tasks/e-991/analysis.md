Today there is no concise way to see 'what is this session, what task is it bound to, where is its worktree, when did it start, how many messages.' 'session list' is global and noisy; 'session history' dumps messages. We need a focused per-session detail command — and crucially, one that defaults to the current session without requiring the user to look up a cryptic UUID prefix.

Behavior:
- 'endless session show' (no arg) → resolve current session via the companion file (E-989). In tmux, 'current' means the Claude pane in this window if there is exactly one, else error and list candidates. Outside tmux, 'current' means the companion file whose pid matches a claude ancestor of the calling shell — fall back to error if ambiguous.
- 'endless session show <id>' → explicit lookup by endless integer id or by Claude UUID prefix.

Output (text mode):
- Endless id, Claude UUID, project, state, started_at, last_activity, message count
- Bound task (id + title) if one is active
- Worktree path + branch + dirty/clean if a worktree is bound
- Most recent summary, flattened to one line (we already do this for 'session list')

Add --json for scripting.

Verification:
- From inside a Claude pane: 'endless session show' returns its own session.
- From a sibling pane in tmux: 'endless session show' returns the Claude session in this tmux window.
- Two Claude panes in window: errors with disambiguation list.
- Explicit id always works regardless of context.