The ordering graph under `session status` (internal/sessionstatuscmd/graph.go,
data from `SessionGraphData` in internal/monitor/session_graph.go) drops a
`precedes` link as soon as the earlier task is spawned. It should keep showing
the link until that task lands.

How it happens today:
- `graphSeeds` excludes every row that is in flight (a live session is on it),
  so a task stops seeding the graph the moment it is spawned.
- `SessionGraphData` then adds back non-terminal tasks that point at a seed,
  but its recursive walk follows only `dep_type = 'blocks'`. A `precedes`
  source is never added back, so the edge and the spawned task disappear
  from the graph.

A `blocks` link survives the same transition because of that walk, which is
the behaviour the user expects from both.

Seen on 2026-10-14: four tasks were linked
E-2333 -> E-2332 -> E-2334 -> E-2331 with `precedes`. Spawning E-2333 removed
it from the graph at once, although it had not landed.

What the user asked for: as with blocked tasks, a spawned task's id stays in
the graph, shown bright, until it lands; only its row in the task table is
dimmed. The renderer currently dims every in-flight id (`lineSegments`, via
`GraphNode.InFlight`). The planning session should check how blocked chains
actually render today, and make `precedes` match.
