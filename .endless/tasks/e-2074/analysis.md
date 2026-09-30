Delete all background-agent support — `task spawn --bg`/`--attach`, `task attach`, `endless agents`, `session-query record-bg-agent`, `spawn-window --attach`, monitor.RecordBgAgentSession/DecorateBgSession/ListBgAgents, bg_agents.go, the sessionkind package and the session_kinds table.

Then drop the four columns: short_id and kind_id (served only background agents), summary (superseded by the on-demand recap of E-1925), and plan_file_path (E-1338, which proposed a home-relative prefix for it, is declined, and its Set/GetPlanFilePath path is vestigial).
