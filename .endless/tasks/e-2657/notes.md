Boundary. This task covers the command lines Endless generates for its tmux servers: `tmux status-line`, `show-menu`, `monitor-wake`, `spawn-launch` and how `Server.Init` aims `tmux init`. `record-windows` already carries its socket.

E-2572's outcome recommended one broader task for all of its departures. Mike asked for this narrower task. These E-2572 findings are outside this task and have no task yet: Claude hooks using whichever server `$TMUX` names, pane identity through the ambient server (`monitor.TmuxServerUUID` and its callers), and live views installing global hooks and pane tags on whichever server holds them.

ED-1673 is the rule: a command may compare `$TMUX` with the configured sockets to learn whether it is in a pane of an Endless server, and must not otherwise use the caller's server.
