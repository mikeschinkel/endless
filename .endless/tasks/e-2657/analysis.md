## Decisions made with Mike while planning (2026-11-18)

- **Pane lookups take the server as a parameter.** Rejected: one socket set per process, because it silently redirects every `monitor` tmux call in that process. Rejected: adding the flag without changing the lookups, because the flag would then have no effect.
- **Generated lines carry `--socket`; without it, a verb that serves either server resolves it from `$TMUX`.** `status-line`, `show-menu`, `active-id` and `monitor-wake` use the configured server whose socket matches `$TMUX` and fail when it matches neither, never using the caller's server. `--socket` wins over `$TMUX` (Mike, reviewing P5: requiring `--socket` was an unintended consequence). Rejected: dropping `--socket` from the lines.
- **`tmux apply` and `tmux init` name their server with `--server tasks|console`.** Rejected: working only from inside a pane of the server, and removing the verbs.
- **The `monitor-wake` hooks move into each server's generated config.** Rejected: keeping the runtime install.
- **Live monitors refuse outside a pane of an Endless server, including outside tmux.** ED-1673 was amended to say so.

## Why this task has value

The generated lines reach the right server today, because tmux gives their jobs the right `$TMUX` (confirmed on a private tmux 3.8 server). The gain is that Endless stops writing its status line, key bindings and hooks into a person's own tmux, conforms to ED-1673, and stops faking `$TMUX` in `Server.Init`.

## Typed commands

`endless task id`, `endless tmux task` and the justfile run `active-id` with no generated line to carry a socket. They compare `$TMUX` with the configured sockets, which ED-1673 allows.

## Consequence of the monitor refusal

Monitors fire the background job runner. A person with no monitor running on an Endless server has background jobs only through `endless jobs run`.
