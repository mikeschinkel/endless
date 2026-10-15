The graph's starting set, the tasks `session status` hands to the graph, is
redefined as every row of the session's table that has not landed and is
either the session's own task or drawn at full brightness and not suppressed.
"Drawn at full brightness" is `rowIntensity` returning normal or bold, the
same rule `colorize` uses for the table, so the graph and the table cannot
disagree about which rows count. "Landed" is the row's `Landed` field (at
least one `task_landings` row). "Suppressed" is `StewardedElsewhere`, whether
or not `--show-suppressed` is on.

`graphSeeds` in internal/sessionstatuscmd/graph.go implements that rule in
place of its current exclusion list. `monitor.SessionGraphData` no longer
drops terminal seeds: `graphSeeds` alone decides the starting set, so the own
task stays in it at a terminal status until it lands.

`SessionGraphData` also adds, one step only, every open task that the
session's own task directly blocks or should precede (a downstream task). It
takes the own task's id as a new argument for that, from the focal row
`gatherGraph` already has. A downstream task's own downstream tasks are not
added.

The recursive walk of open blockers then starts from the starting set AND the
downstream tasks, so every task the graph draws has its open blockers added,
and theirs, in any project, until a task has no open blocker. A terminal
blocker ends the walk, as today. The walk's `UNION` already refuses to add a
task twice, so a blocks cycle terminates; the layout's existing cycle
detection draws it as a cycle line. A test pins both.

`lineSegments` stops dimming in-flight tasks. Only an id's repeat occurrences
on later lines stay dim. The `in_flight` field in `--json` is unchanged.

Tests go in internal/sessionstatuscmd's graph tests and internal/monitor's
session-graph tests, through the existing seams.
