Decisions from the /needs-plan session (Mike, 2026-10-11). The plan waits for E-2240's plan, because it depends on E-2240's server names and fixed socket path.

- Scope: only tmux servers Endless controls are supported (E-2240). Sessions on any other server are out of scope; E-2302 researches filtering hooks to Endless servers.
- Evidence of death: a session is dead when the Endless server it was bound to has gone: that server's fixed socket (`tmux -S <fixed path>`, per E-2240) answers with a different @server_uuid, or nothing is listening on it. No directory scan, no lsof; not being found is never by itself proof.
- Read time only: the session_liveness view reports `dead`; nothing writes sessions.state = 'ended' from a liveness verdict (E-1898's rule stands).
- Steward test: E-2293's stewardship rule switches from `liveness = 'live'` to `liveness != 'dead'`, matching the claim guard (ListLiveSessions).
- State-only readers to switch to `liveness != 'dead'` (today they count a ghost as present):
  - WorktreeInUse (internal/monitor/worktree_inuse.go) — a ghost makes its worktree read "in use";
  - the two `in_flight` expressions in internal/monitor/session_status.go — a ghost marks its task in flight on session status boards;
  - the EXISTS over sessions in internal/monitor/session_graph.go's node query — the graph's "has a live session" flag.
  Pane-to-session lookups (tmux_lookup.go, events/session_status.go, session.go) match on the current server_uuid, so a ghost on a vanished server can't match them; they stay as they are.
- Existing rows bound to the user's default server: to settle when the plan is written, once E-2240 decides what happens to sessions created before it.
