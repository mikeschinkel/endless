Three changes to session status and session monitor (same frame):

1. Hidden tasks still drive duplicate detection. liveOwnership in internal/monitor/session_ownership.go builds owners from session_tasks and never consults session_hidden_tasks. A session that ran `session hide --task E-N` still counts as a live updater, so the task stays ambiguous and the duplicate marker persists on the other session's board. Observed: E-1814 hidden in E-1991's session still showed the marker in ES-1248's. Hiding should take that session out of ownership and duplicate evaluation for that task. E-2188's plan called hide the manual escape hatch but did not state this.

2. Dim rows that are no longer actionable from this board: tasks this session spawned into another session, and parent tasks.

3. The duplicate marker's colors lack contrast. Use foreground 232 on background 160 (256-color), per Mike's swatch; check legibility in light and dark themes.