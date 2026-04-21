# Research Task: Can the Go MCP SDK Support Claude Code's Channel Protocol?

## Objective

Determine whether the official Go MCP SDK can implement a Claude Code channel plugin, or whether we need to drop to raw JSON-RPC or fork the SDK. The answer directly determines our implementation approach for the Endless channel plugin. We must stay in Go — adding TypeScript would introduce a 4th language to our stack (Go, Python, Bash) and we are trying to minimize, not expand.

## Background

Claude Code's "channels" feature (research preview, v2.1.80+) lets an MCP server push events into a running Claude Code session. The protocol is standard MCP (JSON-RPC 2.0 over stdio) with Claude Code-specific extensions:

1. The server must advertise `{"experimental": {"claude/channel": {}}}` in its capabilities during the MCP `initialize` handshake
2. The server pushes events by sending a JSON-RPC notification with method `notifications/claude/channel`
3. Optionally, the server can handle incoming notifications with method `notifications/claude/channel/permission_request` for permission relay
4. Standard MCP tool registration (`tools/list`, `tools/call`) works normally for reply tools

The official channel docs and all examples use the TypeScript MCP SDK (`@modelcontextprotocol/sdk`). We need to know if the Go SDK can do the same.

## SDK Under Investigation

**Official Go SDK only**: `github.com/modelcontextprotocol/go-sdk`
- Package docs: `pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp`
- Maintained by the MCP project in collaboration with Google
- Also check the `jsonrpc` sub-package for lower-level transport primitives

We are **not** considering `github.com/mark3labs/mcp-go` (community SDK). If the official SDK falls short, our preferred path is to fork `go-sdk`, add the needed feature, and submit a PR upstream. See the "Fork Feasibility" section below.

## Specific Questions to Answer

### Q1: Experimental Capabilities Advertisement

During MCP initialization, the server sends a response to the `initialize` request that includes its capabilities. Claude Code channels require:

```json
{
  "capabilities": {
    "experimental": {
      "claude/channel": {}
    }
  }
}
```

**Investigate**:
- Does `mcp.NewServer()` or `ServerOptions` expose a way to set arbitrary experimental capabilities?
- Look at the `ServerCapabilities` struct definition — does it have an `Experimental` field or a catch-all map?
- If not directly exposed, can we hook into the initialization response to inject this field?
- Check the MCP spec (https://modelcontextprotocol.io/specification) for how `experimental` capabilities are formally defined — is it `map[string]any` or a fixed struct?

### Q2: Sending Custom Notifications

Channels push events by sending JSON-RPC notifications with a non-standard method name:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/claude/channel",
  "params": {
    "content": "some event text",
    "meta": {
      "event_type": "plan_completed",
      "plan_id": "123"
    }
  }
}
```

**Investigate**:
- Does the Go SDK's `Server` or `ServerSession` type expose a method for sending arbitrary notifications?
- Look for something like `SendNotification(method string, params any)` or `Notify(...)`
- The TypeScript SDK uses `server.notification({method: "notifications/claude/channel", params: {...}})` — is there a Go equivalent?
- If the SDK doesn't support arbitrary notification methods, can we access the underlying JSON-RPC transport to write raw messages?

### Q3: Receiving Custom Notifications

For permission relay, the server needs to handle incoming notifications with method `notifications/claude/channel/permission_request`:

```json
{
  "method": "notifications/claude/channel/permission_request",
  "params": {
    "request_id": "abcde",
    "tool_name": "Bash",
    "description": "Run ls -la",
    "input_preview": "{\"command\": \"ls -la\"}"
  }
}
```

And respond with a notification:

```json
{
  "method": "notifications/claude/channel/permission",
  "params": {
    "request_id": "abcde",
    "behavior": "allow"
  }
}
```

**Investigate**:
- Can the Go SDK register handlers for arbitrary incoming notification methods?
- The TS SDK uses `server.setNotificationHandler(schema, handler)` with a Zod schema — is there a Go equivalent?

### Q4: Standard Tool Registration

This should work since tools are core MCP, but confirm:
- Can we register tools with `mcp.AddTool()` that have custom input schemas?
- Can we set the `instructions` field on the server (added to Claude's system prompt)?

### Q5: Raw JSON-RPC Fallback Assessment

If Q1-Q3 reveal gaps in the SDK:
- What does the Go SDK's transport layer look like? Can we access the raw `io.Reader`/`io.Writer` for stdio?
- Could we use the SDK for standard MCP features (tools) but write raw JSON-RPC for channel-specific messages?
- Does `github.com/modelcontextprotocol/go-sdk/jsonrpc` provide lower-level primitives we could use directly?
- How much code would a minimal raw JSON-RPC stdio implementation be? (The channel protocol surface is small: one capability, two outgoing notification methods, one incoming notification method, plus standard tool registration.)

### Q6: Fork Feasibility

If the official SDK is missing experimental capability and/or custom notification support:
- How much architectural change would be needed to add it? Is it a matter of exposing an existing `Experimental map[string]any` field, or would it require rethinking how capabilities are serialized?
- For custom notifications, is the internal machinery (JSON-RPC write path) already there and just not exposed publicly, or is it fundamentally missing?
- Would a PR adding this be small and self-contained (likely to be accepted upstream) or large and invasive (better maintained as a fork)?
- Check the SDK's issue tracker and PRs for any existing discussion about experimental capabilities or custom notification support.

## How to Investigate

1. **Read the source code** of `github.com/modelcontextprotocol/go-sdk`. Focus on:
   - `mcp.ServerCapabilities` struct definition
   - `mcp.NewServer()` and its options
   - `mcp.Server` methods for sending notifications
   - `mcp.ServerSession` for per-connection state
   - The `jsonrpc` sub-package for lower-level transport access
   - Any `Experimental` or `Extra` or catch-all fields in capability structs
   - Serialization/deserialization of the initialize handshake

2. **Read the source code** of the TypeScript MCP SDK's `Server` class for comparison:
   - How does `new Server({capabilities: {experimental: {"claude/channel": {}}}})` work internally?
   - How does `server.notification()` serialize to JSON-RPC?

3. **Check the MCP spec** (https://modelcontextprotocol.io/specification) for how `experimental` capabilities are formally defined.

4. **Check the SDK's issue tracker** (`github.com/modelcontextprotocol/go-sdk/issues`) for any existing discussion about experimental capabilities or custom notifications.

5. **Write a minimal proof of concept** if the SDK looks promising — a Go server that:
   - Declares `experimental: {"claude/channel": {}}` in its capabilities
   - Sends a `notifications/claude/channel` notification when it receives an HTTP POST
   - Registers a simple reply tool
   - Runs over stdio

## Deliverable

A research document that answers Q1-Q6 with code references and, if feasible, a working proof-of-concept Go channel server. The document should end with a clear recommendation:

- **Option A**: Use the official Go SDK as-is (if it supports everything)
- **Option B**: Use the official Go SDK for tools + raw JSON-RPC for channel-specific messages (hybrid)
- **Option C**: Fork the official Go SDK, add experimental/notification support, submit PR (if the change is small)
- **Option D**: Fork the official Go SDK and maintain independently (if the change is large but doable)
- **Option E**: Implement the full channel protocol as raw JSON-RPC in Go (if SDK architecture makes forking impractical)
- **Option F**: Use TypeScript (last resort — rejected unless all Go options are unworkable)

Include the trade-offs for each viable option. For Options C and D, include a rough estimate of the scope of the SDK changes needed.
