E-1682 added a durable navigation trail — every tmux focus change appended to
`session_navigations` by a global tmux hook. Its stated purpose was "usability
analysis of navigation, and recovering a session a user lost track of". Neither
materialised: 5,374 rows (98% `via=manual`, roughly 110 rows a day, unbounded —
schema.sql:659 says "pruning deferred"), one reader, and what `session trail`
prints is a list of pane switches the user performed and therefore already knows
about. It was surfaced by a session while designing E-1681, not requested.
Remove it.

Remove:
- `session_navigations` and `nav_via_kinds` tables (schema.sql plus a change
  file dropping both)
- `internal/navvia` package, and its VerifyIntegrity call in
  `monitor/db.go:780-786`
- `monitor.RecordNav`, `ListNavTrail`, `NavEdge` (`internal/monitor/session_nav.go`)
- `internal/tmuxcmd/record_nav.go` and its dispatch case (`tmuxcmd/tmux.go:49`)
- the `client-session-changed` and `session-window-changed` hook installation in
  `internal/tmuxcmd/apply.go`, and the `@endless_nav_via` marker plumbing
  (`_set_nav_via_goto`, `_clear_nav_via` in session_cmd.py)
- `endless session trail`, plus `session_trail` and `_nav_endpoint_label` in
  session_cmd.py

Keep `session back`: its back-stack lives in tmux options, not this table, and
does not depend on any of the above.

Note for whoever takes it: the two signals latent in the table and never
surfaced were thrash (rapid A-B-A bouncing) and abandonment (time since a
session was last focused). Both were judged not worth the table. If either is
ever wanted it should be built from what it needs, not recovered from this one.
