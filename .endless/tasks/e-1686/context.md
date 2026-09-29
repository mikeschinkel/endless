TouchSession's UPDATE branch (session.go) deliberately never touches state.

Since every reader filters state != 'ended' (E-1530 pane-reuse safety), a still-live session goes permanently invisible — blank tmux status line, 'session next'/nav find nothing, and 'task bind' silently binds onto the dead row.

This is the root cause of unreliable session-lifetime tracking.
