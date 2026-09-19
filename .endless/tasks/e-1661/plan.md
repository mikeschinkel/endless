# E-1661 — Hook blocks (exit 2) on integrity/DB failure

Implements ED-1519. Decided (Mike): ALWAYS block, no grace window.

## Steps
1. Confirm Claude Code PostToolUse semantics: exit code 2 feeds stderr to the
   model (agent sees it); any other non-zero is user-only ("non-blocking status
   code"). Use the claude-code-guide agent if unsure.
2. internal/hookcmd/claude.go, the `hook claude` path: when project lookup /
   DB Open() returns an integrity or DB-open error (from internal/monitor/db.go
   VerifyIntegrity, db.go:298-330), exit **2** with the error on stderr, instead
   of the current non-blocking exit. Apply to every hook event that opens the DB
   (PostToolUse, UserPromptSubmit, SessionStart).
3. Keep the message actionable: name the drifted table and the stale-binary
   remedy (rebuild/reinstall).

## Non-goals
No grace window, no transient tolerance (ED-1519). A hard stop while a schema
settles is the intended behavior.

## Verify (tests/tasks/e-1661-verify.sh)
- Seed a temp DB whose task_types drifts from the enum; run the hook binary
  against it; assert exit code == 2 AND stderr contains "integrity check".
- Healthy DB: hook exits 0.
