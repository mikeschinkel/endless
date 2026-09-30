Touch-points: internal/hookcmd/claude.go ('hook claude' exit path -- make integrity/DB-open failures exit 2, not the current non-blocking code); internal/monitor/db.go Open()/VerifyIntegrity error propagation. Confirm Claude Code semantics first (PostToolUse exit 2 -> stderr fed to model AND tool flagged; other non-zero -> 'non-blocking status code', user-only). Verify (per-task script): force a task_types drift in a temp DB and assert the hook process exits 2 with the integrity message on stderr. Incident that surfaced this: brainstorm task-type landing caused a stale-binary integrity skew whose errors were invisible to the agent.

## From the description

DECIDED (Mike): ALWAYS block on integrity failure -- exit 2 from the hook so the agent halts; NO grace window for transient cross-session migration windows (a hard stop while a schema settles is correct and avoids hard-to-unravel ripple effects).

Touch-points in analysis.
