Shipped Hide/Show Endless row menu item. Last item in both the prefix+e and right-click menus; label adapts to current `status` option:

- status=2 (visible) → "Hide Endless row" → sets status to 1
- status=1/"on"/"off" (hidden) → "Show Endless row" → sets status to 2

After toggle, `refresh-client -S` runs so the change is immediate.

`statusRowVisible()` queries `tmux show-options -gv status` to decide the label at menu-build time. Falls back to "visible" on tmux error since `endless tmux apply` always sets status to 2.

Server-scoped limitation: tmux's `status` is one number for the whole server, so the toggle affects every attached client (no per-window granularity). Documented in the task description; the per-window auto-collapse case is E-1259.

When hidden, menu remains reachable via prefix+e and right-click on row-0 status-right.

Build + all Go tests pass. Verification deferred to land+install — exercise: prefix+e → H to hide; row disappears; prefix+e → H to show; row reappears.