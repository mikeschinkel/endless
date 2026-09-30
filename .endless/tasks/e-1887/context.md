monitor.TouchSession is the writer behind sessions.process_id — the single value the tmux status line resolves a pane through — and it runs on every hook event. When it fails, hook.Run log.Printf's the error and exits 0, deliberately, so a broken hook never blocks a tool call.

Claude discards that stderr, so the only symptom is a blank status bar, which is also what a pane with no task looks like.

Session ES-1055 ran that way for four weeks: every PreToolUse died with 'table sessions has no column named process', process_id stayed NULL, and nothing anywhere recorded it.
