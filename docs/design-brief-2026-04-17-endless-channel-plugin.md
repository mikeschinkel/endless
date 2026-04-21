# Design Brief: Endless Channel Plugin for Claude Code

> **Note**: A companion research document (`go-mcp-channel-research-prompt.md`) accompanies this brief. It defines a spike to determine whether the official Go MCP SDK can support the channel protocol, and if not, whether a fork is feasible. The spike should be completed before implementation begins, as its outcome determines the implementation approach.

## Purpose

Build an Endless channel plugin that pushes cross-project awareness events into a running Claude Code session. This gives Claude Code sessions real-time context about what's happening across all Endless-managed projects — plan status changes, dependency completions, and targeted context briefs — without the developer needing to switch terminals or manually check Endless.

## What Are Claude Code Channels?

Channels are a Claude Code feature (research preview, v2.1.80+) that allow an MCP server to push events into a running interactive Claude Code session. Unlike standard MCP servers (which Claude polls on-demand), channels deliver events proactively.

Key facts:

- A channel is an MCP server that declares the `claude/channel` experimental capability
- Claude Code spawns it as a subprocess and communicates over stdio
- Events arrive in the session wrapped in `<channel source="...">` tags
- Channels can be one-way (push alerts) or two-way (push + reply tool)
- Requires claude.ai login (not API key auth)
- Still in research preview; use `--dangerously-load-development-channels` for custom channels

Official docs (read these before starting):
- Overview: https://code.claude.com/docs/en/channels
- Reference (protocol details, examples): https://code.claude.com/docs/en/channels-reference

## Language Choice: Go

Endless is a Go project. The channel plugin must be Go as well — adding TypeScript would introduce a 4th language to the stack (Go, Python, Bash) and we are trying to minimize, not expand.

**The official Go MCP SDK exists**: `github.com/modelcontextprotocol/go-sdk` (maintained with Google). It supports tools, resources, prompts, and stdio transport.

**The risk**: The `claude/channel` capability is a Claude Code extension to MCP, not part of the standard MCP spec. The channel protocol requires setting experimental capabilities and sending custom notifications, which the Go SDK may not expose.

**If the SDK falls short**, the preferred path is to fork `go-sdk`, add the needed features, and submit a PR upstream. If the changes would be too architecturally invasive for upstream acceptance, we'd maintain the fork. Raw JSON-RPC in Go is the fallback if even forking is impractical. TypeScript is rejected unless all Go options fail.

We are **not** considering `github.com/mark3labs/mcp-go` (community SDK).

**The companion research document defines the spike.** Do not begin implementation until the spike is complete and an approach is selected.

## Architecture

```
┌──────────────────────┐
│  Endless CLI / DB    │
│  (plan changes,      │
│   status updates)    │
└──────────┬───────────┘
           │ HTTP POST to localhost (targeted, on-demand)
           ▼
┌──────────────────────┐
│  Endless Channel     │     stdio (MCP JSON-RPC)
│  Plugin Server       │◄──────────────────────────► Claude Code Session
│  (Go binary)         │
└──────────────────────┘
```

The channel plugin runs as a local process spawned by Claude Code. It:

1. Listens on a localhost HTTP port for events from the Endless CLI/daemon
2. Translates those events into MCP channel notifications
3. Pushes them into the Claude Code session over stdio
4. Exposes a reply tool so Claude can update plan status

## Token Efficiency: HTTP-Push Primary, DB as Replay Fallback

**Primary delivery**: Endless CLI pushes events to the channel plugin via HTTP POST. This is targeted — events only fire when something actually happens. No polling, no wasted tokens on "nothing changed" cycles.

**Fallback/replay**: If a Claude Code session wasn't running when an event occurred, the channel plugin can check the Endless DB on startup for missed events since the session's last known timestamp. This is a one-time catch-up, not continuous polling.

**Why not DB-primary**: Polling the DB on an interval would generate channel notifications on every cycle regardless of whether anything relevant happened. Each notification consumes tokens when Claude processes the `<channel>` tag. For a system managing 10+ projects, the noise-to-signal ratio would be terrible. HTTP POST ensures every token spent on a channel event corresponds to an actual state change.

**Design rule**: Every channel notification should represent an actionable state change. If Claude can't do anything useful with the information, don't send it.

## Notification Format

Each event is pushed as an MCP channel notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/claude/channel",
  "params": {
    "content": "Plan 123 \"Implement channel plugin\" has been marked complete. This was a dependency of plan 456 \"End-to-end integration test\" in project endless-tests.",
    "meta": {
      "event_type": "plan_completed",
      "plan_id": "123",
      "project": "endless"
    }
  }
}
```

This arrives in Claude's context as:

```xml
<channel source="endless" event_type="plan_completed" plan_id="123" project="endless">
Plan 123 "Implement channel plugin" has been marked complete.
This was a dependency of plan 456 "End-to-end integration test" in project endless-tests.
</channel>
```

**Meta key restrictions**: Keys must be identifiers (letters, digits, underscores only). Keys with hyphens or special characters are silently dropped by Claude Code.

**Content should be concise**: Claude reads the full content. Keep it to what's actionable — don't dump the entire plan tree.

## Event Types

### Priority 1 (build first)

- **plan_completed**: A plan was marked done. Include which downstream plans were depending on it.
- **plan_blocked**: A plan hit a blocker. Include the reason and affected downstream plans.

### Priority 2 (add when proven useful)

- **context_brief**: Targeted cross-project orientation. NOT a full status dump. Specific use cases:
  - Sessions working on subplans that feed into a parent plan in another project
  - Sessions using Endless itself to report back to a main session working on Endless (meta-dogfooding, but models a general use-case where tool X is being used to build tool X)
  - Triggered explicitly by `endless plan brief <plan_id>` or similar, not automatically on every session start
- **dependency_resolved**: A specific dependency of the current project's plans was resolved elsewhere.

### Deferred (wait for real demand)

- **plan_created**: A new plan was created that's relevant to the current project.
- **conflict_detected**: Two sessions appear to be modifying overlapping areas.

## Reply Tool

Expose a tool so Claude can report status back to Endless:

```
Tool: endless_update
Arguments:
  - plan_id (string, required): The plan ID to update
  - status (string, required): "in_progress" | "completed" | "blocked"
  - note (string, optional): Brief note about what was done or why blocked
```

Implementation calls back to Endless — either via `endless plan update` CLI or direct DB write.

## Server Instructions

The `instructions` field in the MCP server constructor is added to Claude's system prompt. Keep it focused:

```
Events from Endless arrive as <channel source="endless" event_type="..." plan_id="...">.
These provide cross-project awareness. Read the event_type to understand what happened.

- plan_completed: A plan finished. If it was a dependency of your current work, you may be unblocked.
- plan_blocked: A plan is stuck. If your work depends on it, note the blocker and adjust.
- context_brief: Orientation context for your current session. Use it to inform your approach.

When you complete or block a plan, call the endless_update tool to notify other sessions.
Do not call endless_update speculatively — only when you have actually changed a plan's status.
```

## Permission Handling

A session that receives "your dependency is unblocked" and then immediately stalls on a permission prompt defeats the purpose. There are two complementary approaches.

### MVP: Permissive tool approval for trusted sessions

Launch sessions with appropriate `--allowedTools` or `--permission-mode acceptEdits` so common tool calls don't prompt at all. For trusted local sessions where you control the environment, this avoids the problem entirely with zero channel work. Example:

```bash
claude() {
    command claude \
        --dangerously-load-development-channels server:endless \
        --permission-mode acceptEdits \
        "$@"
}
```

This is sufficient to start and avoids building permission relay infrastructure before the core channel loop is proven.

### Enhancement: Permission relay through the Endless channel

The channel protocol supports a `claude/channel/permission` capability that forwards tool approval prompts to a remote destination. When Claude wants to run a tool that needs approval, the prompt gets forwarded through the channel, and you can approve/deny from your phone or another terminal.

This is more powerful than blanket `acceptEdits` because it gives you per-call control without being at the terminal. Build this after the core event loop is working and the `acceptEdits` approach has been tested. The research spike (Q3 in the companion doc) already investigates the SDK support needed for incoming notification handlers, so the groundwork will be laid.

## Configuration

### MCP Server Registration (.mcp.json)

The channel plugin registers in `.mcp.json` like any MCP server:

```json
{
  "mcpServers": {
    "endless": {
      "command": "/path/to/endless-channel",
      "args": [],
      "env": {
        "ENDLESS_CHANNEL_PORT": "9200",
        "ENDLESS_DB_PATH": "~/.endless/endless.db"
      }
    }
  }
}
```

This can live at the project level (`.mcp.json` in project root) or user level (`~/.claude.json`). Since Endless is a cross-project tool, user-level makes more sense so it's available in every project.

### Channel Activation

During the research preview, channel activation requires a CLI flag. The `--channels` and `--dangerously-load-development-channels` flags are **CLI-only** — they cannot be set in `settings.json` or other config files yet.

The `channelsEnabled` managed setting is the org-level toggle. Per the docs, Pro and Max users without an organization skip this check entirely. If your subscription is treated as an org (Max plans get an auto-generated org), `channelsEnabled` should work — Endless will be the primary way you use Claude, so enabling it org-wide makes sense. There was a known bug (GitHub issue #36460, ~March 2026) where personal Max plans with auto-generated orgIds were incorrectly blocked from channels. Test this early.

To avoid remembering the CLI flag every time, add a shell function to `~/.zshrc` (or a sourced file like `~/.zsh/functions`):

```zsh
claude() {
    command claude \
        --dangerously-load-development-channels server:endless \
        "$@"
}
```

This wraps the real `claude` binary, injects the channel flag, and passes through any other arguments normally. Once channels graduate from research preview and config-file activation becomes available, remove the wrapper.

### Auto-Registration

The channel plugin should auto-register with Endless on startup. When the plugin starts (spawned by Claude Code), it:

1. Determines the current project from the working directory
2. POSTs a registration to the Endless daemon/API with its port and project
3. Endless routes relevant events to that port

A flag to disable auto-registration may be needed later, but don't build that yet.

## Multi-Session Routing

Multiple Claude Code sessions = multiple channel plugin instances, each on its own port.

**Approach**: Broadcast + filter. Each channel instance:
- Registers with Endless on startup (port + project)
- Receives all events from Endless
- Filters to events relevant to its project before emitting notifications

This is simplest and avoids a complex routing layer. If broadcast becomes noisy, add server-side filtering later — but let usage prove the need.

## Constraints and Caveats

- **Research preview**: The channels protocol may change. Pin to a specific Claude Code version for stability.
- **claude.ai login required**: Channels do not work with API key or Console auth.
- **Events only arrive while session is open**: No built-in queuing. The DB-replay-on-startup pattern handles this.
- **Meta key format**: Letters, digits, underscores only. No hyphens.
- **CLI flag required**: No config-file activation during research preview. Use the shell function wrapper.

## Deliverables

1. **Go MCP SDK spike** (see companion research doc — do this first)
2. Channel plugin binary in Go
3. Integration point in Endless CLI for pushing events (e.g., `endless notify` or automatic hooks on plan status changes)
4. DB-replay logic for catching up on missed events at session start
5. `.mcp.json` snippet for user-level registration
6. README documenting setup
