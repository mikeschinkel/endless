# Tmux Integration

Endless ships a tmux integration that puts the active task ID, project, and status on a second status row, plus popup menus for common actions.

```bash
endless tmux apply              # configure the running tmux server (ephemeral)
endless tmux status-line        # the runtime printer tmux calls per refresh
```

After `apply`, your tmux session shows a second status row formatted like:

```
  [E-1248] · endless · in_progress
```

The bar reflects whichever Claude session owns the current tmux pane (with a session-scoped fallback so non-Claude panes don't blank).

## This feature is evolving fast

The tmux integration is being **actively expanded**. Menus, hotkeys, layout, permanent install, theming, and lineage breadcrumbs are all in flight at the time this guide was written. **Don't memorize the UI** — run `endless tmux --help` for the currently shipping verbs and trust the help over any documentation more than a few days old.

## Lifespan

`endless tmux apply` configures the running tmux server *ephemerally* — the configuration survives until tmux restarts. Permanent install (companion file + `endless setup tmux-status` verb) is a separate feature, not yet shipped.

## See also

- `endless guide spawn` — `task spawn` creates a new tmux window for a task
- Run `endless tmux --help` for current commands and flags
