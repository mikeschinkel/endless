Resolves <ref> (a task id as shown on the tmux tab, a session id, or a Claude UUID prefix) to the session's UUID and its task worktree, cd's into the worktree, and execs `claude --resume <uuid>` so the resumed session takes over the current pane.

Resolution is task-first and, unlike `session goto`, includes ended sessions, since a crash takes every session to a dead pane.

The task->session lookup runs in Go via a `session-query resume-target` verb (no new Python SQLite reads).
