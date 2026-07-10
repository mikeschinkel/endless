# Plan: Endless MCP Channel Plugin (#697 — Phase 2)

## Context

Phase 1 (complete) built the `endless channel` CLI commands (beacon/connect/send/inbox/close) with tmux send-keys for delivery. Dogfooding revealed that send-keys corrupts the user's input buffer when they're mid-typing. Claude Code's MCP Channels feature (research preview) provides clean push delivery via stdio — no terminal interference. This phase replaces the tmux nudge with an MCP channel plugin.

## Decision Log

- **2026-04-17**: CLI renamed from `endless msg` to `endless channel`
- **2026-04-17**: MCP channel plugin will replace tmux send-keys delivery. Beacon/connect/send/inbox/close workflow unchanged.
- **2026-04-17**: Go MCP SDK fork approach — cherry-pick PR #844 (`SendNotification` with `x-notifications/` convention)
- **2026-04-17**: Fork URL: `github.com/mikeschinkel/go-mcp-sdk` (user handles all GitHub operations)
- **2026-04-17**: Session identity: TMUX_PANE primary, port file fallback for non-tmux
- **2026-04-17**: Keep hook's UserPromptSubmit message check as fallback for sessions without plugin

## Architecture

```
Session A (sender):                          Session B (receiver):
  endless channel send "msg"                   endless-channel (MCP server)
    → inserts into msg_queue                     ← HTTP POST on localhost:{port}
    → looks up target's channel_port             → pushes MCP channel notification
    → HTTP POST localhost:{port}/notify            via stdio to Claude Code
                                                 → Claude sees <channel source="endless">
                                                 → Claude runs: endless channel inbox
```

Each Claude Code session spawns its own `endless-channel` MCP server on a unique port. The Python CLI delivers messages by POSTing to the target session's port instead of using tmux send-keys.

## Prerequisites (User does manually — no GitHub pushes from Claude)

### FORK — Create Go MCP SDK fork
1. Fork `github.com/modelcontextprotocol/go-sdk` to `github.com/mikeschinkel/go-mcp-sdk`
2. Cherry-pick PR #844's commit (ajuijas's `SendNotification` with `x-notifications/` convention)
3. Tag or note the commit hash for the `go.mod` replace directive

## Implementation Steps

### SCHEMA — Add channel_port column
- `sql/schema.sql`: Add `channel_port INTEGER` to ai_sessions (after `tmux_pane`)
- `internal/monitor/db.go`: Add migration (PRAGMA table_info check, ALTER TABLE) — same pattern as tmux_pane migration
- `internal/monitor/session.go`: Add `SetChannelPort(sessionID string, port int) error` and `GetChannelPort(sessionID string) (int, error)` — same pattern as SetTmuxPane

### PLUGIN — Build the MCP channel server binary
- New file: `cmd/endless-channel/main.go`

**Startup sequence:**
1. Create MCP server with `claude/channel` experimental capability
2. Set server instructions (see below)
3. Pick a free localhost port (`net.Listen("tcp", "127.0.0.1:0")`)
4. Register identity:
   - If `$TMUX_PANE` set: poll `ai_sessions` until a row with matching tmux_pane and state != 'ended' appears, then write channel_port
   - If no `$TMUX_PANE`: write port file to `~/.config/endless/channel-ports/{pid}.json` with `{"port": N, "pid": P, "cwd": "/path"}`. The hook's SessionStart reads matching port files and writes channel_port.
5. Start HTTP server on the chosen port
6. Connect MCP server to stdio transport
7. On shutdown (SIGTERM/SIGINT): clear channel_port from ai_sessions, remove port file if created

**HTTP endpoint:**
- `POST /notify` — receives JSON `{"event": "message", "channel_id": "abc", "preview": "..."}`
- Handler calls `session.SendNotification(ctx, "notifications/claude/channel", params)` where params has:
  - `content`: the preview text (e.g., "You have a pending inter-session message. Run: endless channel inbox")
  - `meta`: `{"event_type": event, "channel_id": channel_id}`

**Server instructions:**
```
Events from Endless arrive as <channel source="endless" event_type="..." channel_id="...">.
When you see event_type="message", run: endless channel inbox
When you see event_type="connected", a session has connected to your channel.
Do not call endless channel inbox unless prompted by a channel event or the user.
```

**Reply tool (optional, Priority 2):**
- `endless_update` — lets Claude update plan status. Deferred to later unless trivial to add.

### DELIVER — Replace tmux nudge with HTTP POST in Python CLI
- `src/endless/channel_cmd.py`:
  - Replace `_tmux_nudge(pane, text)` with `_channel_notify(target_session_id, event, channel_id, preview)`
  - New function queries `ai_sessions.channel_port` for the target session_id
  - POSTs JSON to `http://localhost:{port}/notify`
  - Falls back to tmux send-keys if channel_port is NULL (graceful degradation during transition)
  - Update `connect()`: resolve target_session_id (already available as `row[0]["session_a"]`), call `_channel_notify`
  - Update `send()`: resolve target_session_id from channel, call `_channel_notify`

### HOOKFIX — Update hook fallback
- `cmd/endless-hook/claude.go`:
  - In UserPromptSubmit: check `GetChannelPort(sessionID)`. If port > 0 (plugin running), skip message check (plugin handles delivery). If port == 0, check `HasPendingMessages` and tell Claude to run inbox. This is already partially implemented — just add the port check guard.
  - In SessionStart: if not in tmux, scan `~/.config/endless/channel-ports/` for port files matching CWD, write channel_port to ai_sessions

### DEPS — Go module dependencies
- `go.mod`: Add `github.com/modelcontextprotocol/go-sdk` + replace directive to fork
- Run `go mod tidy`

### BUILD — Justfile updates
- Add `go build -o bin/endless-channel ./cmd/endless-channel` to build recipe
- Add symlink to install recipe

### SETUP — MCP server registration
- `src/endless/setup.py`: Add `setup_channel_plugin()` that writes to `~/.claude.json`:
  ```json
  {"mcpServers": {"endless-channel": {"command": "/usr/local/bin/endless-channel"}}}
  ```
- `src/endless/cli.py`: Add `@setup.command("channel-plugin")` and `remove-channel-plugin`
- Note: User must launch Claude Code with `--dangerously-load-development-channels server:endless-channel` during research preview. Document in setup output.

## Files to Modify

| File | Change |
|------|--------|
| `cmd/endless-channel/main.go` | **NEW** — MCP channel server binary |
| `src/endless/channel_cmd.py` | Replace `_tmux_nudge` with `_channel_notify`, keep tmux fallback |
| `internal/monitor/session.go` | Add `SetChannelPort`, `GetChannelPort` |
| `internal/monitor/db.go` | Migration for `channel_port` column |
| `sql/schema.sql` | Add `channel_port` to ai_sessions |
| `cmd/endless-hook/claude.go` | Add port check guard in UserPromptSubmit fallback; port file scan on SessionStart |
| `go.mod` | Add MCP SDK dependency + replace directive |
| `src/endless/setup.py` | Add channel plugin setup/remove |
| `src/endless/cli.py` | Add setup subcommands |
| `justfile` | Add build/install targets |

## Verification

1. **Prerequisites**: User has created fork with PR #844 cherry-picked
2. **Build**: `just build` — all binaries compile including `endless-channel`
3. **Install**: `just install` — symlinks created
4. **Setup**: `endless setup channel-plugin` — MCP config written
5. **Launch**: `claude --dangerously-load-development-channels server:endless-channel`
6. **Verify plugin**: Check MCP server appears in `/mcp` output within Claude session
7. **Identity**: `sqlite3 ~/.config/endless/endless.db "SELECT channel_port FROM ai_sessions WHERE state='working'"` — should show a port
8. **End-to-end**: Open two Claude sessions, each with channel plugin
   - Session B: `endless channel beacon`
   - Session A: `endless channel connect`
   - Session A: `endless channel send "hello"`
   - Session B should see `<channel source="endless">` notification and run inbox
   - Session B: `endless channel send "reply"`
   - Session A should see the reply notification
9. **Fallback**: Test without channel plugin — hook's UserPromptSubmit should still tell Claude to check inbox
