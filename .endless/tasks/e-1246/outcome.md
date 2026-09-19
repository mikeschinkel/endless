Shipped all four items:

1. Menu title — now `Endless [E-NNN]` resolved at click time via the new `endless-tmux show-menu` subcommand. Falls back to plain `Endless` when no active task is found in the focused window.

2. `Detail` → `Task Details` label rename.

3. Prefix+e popup centered with `-x C -y C` on the focused pane. Right-click variants (MouseDown3StatusRight / M-MouseDown3StatusRight) still anchor to the mouse position (`-x M -y M`) so the menu appears at the click point.

4. Status-line printer output extended to: `[E-NNN] · project · type · phase · tier · status`. Empty/nil fields are skipped so the row stays compact (tier defaults to NULL or 0 and is omitted for tasks without one). `ActiveTaskInfo` struct gained Type, Phase, Tier (the last as *int64 for SQL NULL handling); SELECT extended accordingly.

Architectural improvement: menu construction is now in Go (`cmd/endless-tmux/show_menu.go`), so future menu changes don't require re-running apply — only the binary needs to be rebuilt. Bindings are simple `run-shell '<bin> show-menu --pane=#{pane_id} --position=center|mouse'` calls.

Post-merge action: same as E-1236 / E-1245 — `just install` + `endless tmux apply` from main to refresh the running tmux server.