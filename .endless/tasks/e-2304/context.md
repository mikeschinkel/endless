`PreCompact` is a valid Claude Code hook event — confirmed against the hooks
reference (code.claude.com/docs/en/hooks) on 2026-10-12, which lists 32 event
names usable as keys under `hooks`. The Claude hook dispatch in
`internal/hookcmd/claude.go` has a `case "PreCompact"` that calls
`monitor.ParseTranscript`, the parse whose whole point is to capture the
transcript BEFORE compaction rewrites it. But `CLAUDE_HOOK_EVENTS` in
`src/endless/setup.py` does not list it, so it is never a key in the user-scope
Claude settings file, the harness never calls the hook for it, and the parse
never happens. Every compaction on every machine loses whatever that parse would
have recorded.

Separately and in the same dispatch: `case "ExitPlanMode"`. That is not one of
the 32 event names — it is a TOOL name — so the case is reachable only from a
payload whose `hookEventName` is `"ExitPlanMode"`, which nothing sends. It has
been unreachable since it was written.

Both were found while answering whether the installed hook set needs drift
detection (E-1398, superseded by E-2285). They are the same shape as the
condition that prompted that question: a handler exists, the event is not
installed, and nothing anywhere says so.
