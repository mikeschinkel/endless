Add a menu item `Hide Endless row` that runs `tmux set-option -g status 1`; when the row is hidden, the same menu item flips to `Show Endless row` (runs `status 2`).

Tmux `status` is server-scoped so this is global on/off, not per-window — the auto-collapse-when-empty case is filed separately as a later task.
