Diagnosis from the gomion E-2546 session, 2026-11-03 to 2026-11-05, from Mike's screenshot of session status with E-2546 completed yet bright in the graph line while its table row was dim.

**What the graph does today** (internal/sessionstatuscmd/graph.go):
- graphSeeds, which picks the rows the graph starts drawing from, skips any row whose task has a landing record before it checks whether the row is the session's own task. So a landed own task leaves the graph entirely: the E-2206 complaint this task was filed on. E-2546 stayed visible only because a brainstorm never lands.
- lineSegments is the only place an id is dimmed, and its sole rule is a `seen` map: the first occurrence of an id on the rendered lines is bright, every later one dim, in-flight ids exempt. Status is never consulted. In the screenshot E-2540 drew dim on the second line only because it had drawn on the first; E-2546 drew bright because it was a first occurrence.
- monitor.SessionGraphData filters predecessors with status NOT IN the terminal set, so the own task is the only terminal node that reaches the renderer. monitor.GraphNode carries Status but not whether the task has landed.

**Mike's rulings in the E-2546 session, 2026-11-05:**
- Bright means "we need to work on it"; dim means "no need, because it is landed or being handled elsewhere". Intensity is not about blocking: a task at status `landed` still blocks its dependents but draws dim, because nothing remains for the user to do on it.
- The own task is the "You are here" pointer on a mall map. Removing it makes the user lose their bearings. It is never removed, only dimmed when terminal or landed.
- Repeated ids must all draw alike, bright or dim as the task's own state dictates. A reader may see one copy and not the other, so dimming later copies misleads. This reverses the repeat-dimming half of the earlier E-2345 rule; its in-flight-stays-bright half survives because an in-flight task is neither landed nor terminal.

**Rejected alternatives:**
- Keep removing a landed own task and only dim a terminal one that never landed (the brainstorm case). Rejected: leaves the original complaint in place.
- Drop the landed check for every row, not only the own task. Rejected: other sessions' landed tasks would enter the graph, which the earlier work excluded on purpose, and the own task is the only row Mike wants treated specially.
- Tie dimming to blocking (status terminal only, `landed` bright). Rejected by Mike's definition of bright and dim above.

Mike's reason for the task: seeing his own completed task bright made him think it still blocked E-2547, until he realised he was in E-2546 and had completed it. E-2248 is the related monitor fix (rows handled elsewhere, blocker lines) and does not touch this.
