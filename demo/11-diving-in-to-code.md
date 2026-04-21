# 11. Into the Code

## Hook Handler — the brain
`cmd/endless-hook/claude.go`
```go
switch payload.EventName {
case "SessionStart":
    return handleTaskContextInjection(projectID, payload)
case "PreToolUse":
    return handlePreToolUse(projectID, isRegistered, payload)
case "PostToolUse":
    return handlePostToolUse(projectID, payload)
case "ExitPlanMode":
    return handleExitPlanMode(projectID, payload)
case "Stop":
    _ = monitor.IdleSession(payload.SessionID)
case "SessionEnd":
    _ = monitor.EndSession(payload.SessionID)
}
```

## Hook Configuration
`~/.claude/settings.json`
```json
{
  "hooks": {
    "SessionStart":     [{ "command": "endless-hook claude" }],
    "PreToolUse":       [{ "command": "endless-hook claude" }],
    "PostToolUse":      [{ "command": "endless-hook claude" }],
    "Notification":     [{ "command": "claude-tmux-alert on" }],
    "UserPromptSubmit": [{ "command": "claude-tmux-alert off" }]
  }
}
```

## tmux — session multiplexing
`~/.tmux.conf` / `~/.init/tmux/tmux.conf`
- Every project gets its own window
- Spawn opens new windows with `tmux new-window` + `send-keys`
- Window alerts: tab turns red when Claude needs attention
- `@endless_task_id` stored as tmux window option
- Right-click menu to dismiss alerts

## tmux Window Notifier
`~/.init/bin/claude-tmux-alert`
```
claude-tmux-alert on   — set window tab red + prefix with *
claude-tmux-alert off  — clear the alert
```
Reads `TMUX_PANE` from environment, resolves to window, sets `@window_color`.

## Build Recipes
`justfile`
```shell
just build     # templ generate + tailwind CSS + Go binaries
just install   # build + symlink + install Python CLI
just test      # Python tests
just dev       # watch mode (templ + tailwind)
just db-export # export project data for version control
```

## Key File Map
```
cmd/endless-hook/claude.go   — Hook event handler
cmd/endless-channel/main.go  — MCP channel server
cmd/endless-serve/main.go    — Web dashboard server
internal/monitor/            — DB, sessions, tasks
internal/web/                — Dashboard (Go + templ + HTMX)
src/endless/                 — CLI commands (Python + Click)
sql/schema.sql               — Database schema
```
