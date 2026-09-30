internal/monitor/tmux_lookup.go (queryActiveTaskForPanes) joins sessions to tasks on bare 'process' (= tmux pane id) string, orders by last_activity, and returns the top row.

Observed: pane %186 in the current server resolves to E-1337 because sessions 482 and 484 (2026-05-21, process='%186', active_task=1337) were never marked ended when their server died. ~9 currently-live panes have ghost rows.
