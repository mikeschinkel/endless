`session status`'s ordering graph never draws the chain that blocks the
session's own task.

`graphSeeds` in internal/sessionstatuscmd/graph.go decides which tasks the
graph starts from. By E-2164's design it drops the focal (viewing) task, the
spawning task, the parent row, and every row another live session stewards.
`monitor.SessionGraphData` then adds every open blocker of those starting
tasks, in any project. With no starting tasks, it adds nothing.

Seen on 2026-10-15 from E-2333's session. E-2333 is blocked by E-2208
(go-cfgstore), and E-2208 is blocked by E-2206 (go-cfgstore, claimed by another
live session). The table marks E-2333 as blocked, and `--show-suppressed`
lists E-2206 and E-2208 as suppressed rows. Yet `endless session status
--graph` prints "no ordering relations among this session's tasks", because
every row was excluded from the starting set: E-2333 is focal, E-2240 is the
spawning task, and E-2206 and E-2208 are stewarded elsewhere.

The graph is not limited to one project. The node walk follows `blocks` edges
regardless of project, so once the starting set includes the focal task, the
cross-project chain should draw as it is.

E-2164 (the graph) and E-2293 (suppression) are shipped (`assumed`). This
extends them, so per ED-1550 it is a new task rather than a reopen.
