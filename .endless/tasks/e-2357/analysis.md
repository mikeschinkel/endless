Diagnosis from the gomion E-2546 session, 2026-11-03, Mike's screenshot of session status with E-2546 completed yet bright in the graph line while its table row was dim.

Where the graph decides intensity, in internal/sessionstatuscmd/graph.go:
- graphSeeds keeps the session's own task in the starting set at any status (the E-2345 rule: the chain blocking it is what the viewer most needs). So a completed own task is still drawn. That is the half of this task already done.
- lineSegments is the only place an id is dimmed, and its sole rule is a seen map: the first occurrence of an id on the rendered lines is bright, every later one dim. Status is never consulted. In the screenshot E-2540 drew dim on the second line only because it had drawn on the first; E-2546 drew bright because it was a first occurrence.
- monitor.SessionGraphData filters predecessors with status NOT IN the terminal set, so the own task is the only terminal node that can reach the renderer. The fix is therefore one condition: in lineSegments, an id draws dim when it was already seen or when g.nodes[id].Status is terminal (taskrow.IsTerminal, the same predicate rowIntensity uses for the table). In-flight ids stay bright as E-2345 requires.
- The --json nodes already carry status, so the data is in hand; no query change.

Mike's reason today: seeing his own completed task bright made him think it still blocked E-2547, until he realised he was in E-2546 and had completed it. E-2248 is the related monitor fix (rows handled elsewhere, blocker lines) and does not touch this condition.