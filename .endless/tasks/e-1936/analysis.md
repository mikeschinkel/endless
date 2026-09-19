# Analysis

## Why this is being asked now

E-1898 rebuilt session→pane binding onto a `processes` table keyed on
`(kind, server_uuid, address)`, because the old `sessions.process` string (a
bare tmux pane id) is not unique across tmux server lifetimes — the root cause
of the 2026-08-05 incident that nulled live pane bindings project-wide.

`channels` keys on that SAME string (`INSERT INTO channels (process, port, pid,
created_at)`, `DELETE FROM channels WHERE process=?`, `SELECT port, pid FROM
channels WHERE process=?` — internal/monitor/session.go:274-308). E-1898
deliberately left it alone rather than migrate a surface believed dead, so
after E-1898 lands `channels` is the last consumer of the old non-unique key.

## The assumption that needs confirming first

"channels is vestigial" is Mike's read, and it may be wrong. The
`endless-channel` MCP server is still wired into live Claude sessions — its
instructions ship in the session system prompt:

    Events from Endless arrive as <channel source="endless-channel" ...>.
    When you see event_type="message", run: endless channel inbox

So the surface is at minimum still *installed*. Whether it is ever *exercised*
is the empirical question.

## What to brainstorm

1. **Confirm the premise.** Does anything read/write `channels`? Is the MCP
   server ever actually invoked? Check the ledger, the errors log, and whether
   any session has ever run `endless channel inbox` unprompted.

2. **Re-read the MCP standard.** It has changed recently; capabilities that
   were impossible when endless-channel was built may now be available.
   Reference Mike watched: https://youtube.com/watch?v=oqp6D-ugtX4

3. **Evaluate `SendMessage`.** Claude Code exposes a SendMessage tool that
   continues a previously spawned agent by ID/name with its context intact.
   That may be a better transport for inter-session communication than a
   bespoke MCP server plus a port/pid table — it needs no listener, no port
   allocation, and no process-keyed row that can go stale.

4. **Decide.** Three live options:
   - retire `channels` + the MCP server outright;
   - migrate `channels.process` onto `processes.process_id` (only worth it if
     the surface is genuinely used);
   - rebuild inter-session comms on SendMessage and retire the rest.

## Out of scope

Implementation. The outcome is a decision plus a follow-on retirement or
design task.
