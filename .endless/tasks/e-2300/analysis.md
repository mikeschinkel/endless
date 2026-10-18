Decisions from the /needs-plan session (Mike, 2026-10-11 to 2026-10-17).

- Scope: only tmux servers Endless controls are supported (E-2240); spawns no longer fall back to other servers (E-2385); E-2302 researches filtering hooks.
- Why record the socket path: a binding stores only its server's id. A fixed socket path (E-2240) names Endless's servers, but not which server a given binding was on, so without the record a vanished id could only be judged by not being found, the 2026-08-05 kind of judgment. Recording (uuid, path) lets the check ask the exact socket a session used.
- Why a server with no @server_uuid counts as different: the session's own server had an id in memory, and tmux options do not disappear from a running server.
- Missing socket file reads unknown (Mike chose A): a server can outlive its socket file (macOS `/tmp` cleanup), and judging it dead would let a running session's worktree be claimed. The cost: a server that exits and is never replaced leaves its sessions unknown until a new server takes the path (`endless start`), which is why the refusal names `endless start`.
- Read time only (Mike, Q3 = A): a wrong verdict corrects itself on the next refresh.
- Steward test switches to "not dead" (Mike, Q4 = B): one presence test everywhere.
- State-only readers (WorktreeInUse, in_flight, session graph) counted ghosts as present; they switch to "not dead".
- Existing rows stay unknown (Mike, Q5 = A); recovery is `session end` (Mike, Q6 = B), guarded by both a recency refusal and a y/N prompt (Mike, Q7 = C). Recency guards against ending a session still running on an unreachable server; the prompt guards against a wrong id. `session resume` already resumes ended sessions, so an accidental end is recoverable, though the task can be claimed meanwhile.
- Rejected: lsof/process-table enumeration of tmux servers (unnecessary once only Endless's servers are supported); writing `ended` from a verdict.
