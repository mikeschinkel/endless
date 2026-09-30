session goto switches focus to a LIVE pane and errors if the task/session has none; session resume relaunches in the CURRENT pane (clobbers it).

Neither covers 'take me to session/task X, resuming it in a fresh window if it isn't live' — today that means manually finding the session in the DB, opening a tmux window, and resuming.
