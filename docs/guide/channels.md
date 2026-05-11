# Inter-Session Channels

Endless supports messaging between concurrent Claude Code sessions via channels. Useful when multiple sessions are working on related tasks in the same project.

## Basic flow

```bash
# Session A: advertise availability
endless channel beacon

# Session B: pair with the beacon
endless channel connect                          # auto-detects if one beacon exists
endless channel connect <channel_id>             # explicit ID if multiple beacons

# Either side: send a message
endless channel send "E-839 is done; task update now accepts --phase"

# Either side: read incoming messages
endless channel inbox

# List active beacons for the project
endless channel list
endless channel list --project <name>

# Tear down
endless channel close
```

## How it works

- One session calls `beacon` to register as available.
- Another session calls `connect` to pair with it.
- Messages are delivered via MCP notifications. The receiving session sees a channel event and is expected to run `endless channel inbox` to read it.
- Channels are project-scoped: `connect` with no argument finds the beacon for the current project.

## Receiving messages

When a channel event arrives, the receiving session runs `endless channel inbox` to read pending messages. The MCP plugin surfaces this as a notification — the session does not need to poll.

Do not run `endless channel inbox` unprompted. Only run it when:

- A channel event is delivered.
- The user asks you to check.

## Common patterns

**Handoff.** Session A finishes a blocking task, sends:

> "E-839 confirmed — task update now accepts --phase. You can unblock E-845."

Session B reads, runs `endless task deps E-845`, sees E-839 is now resolved, proceeds.

**Coordination.** Session A is working on database migration; Session B is working on the calling code:

> "About to push migration that renames column `foo` → `bar`. Hold for 5 min on E-851 callers."

**Discovery.** Session A finds a related bug:

> "Filed E-867 — saw a similar issue in the calling code while working on E-845."

## When not to use channels

- For durable handoffs across sessions that aren't both alive simultaneously, file a task instead.
- For decisions, record a decision (see `endless guide decisions`).
- For status that anyone in the project needs to know, update the task field directly.

Channels are for *live* coordination. Persistent state lives in the DB.

## See also

- `endless guide spawn` — spawning a fresh session for a task
- `endless guide tasks` — `task chat` for a chat-only session
