Bindings now capture `#{mouse_x}` and `#{mouse_y}` at binding time and forward them to `endless-tmux show-menu --mouse-x= --mouse-y=`. The Go binary passes them as numeric tmux display-menu coordinates instead of the `M` shorthand (which doesn't work once we shell out through run-shell).

Required side change: the right-click `run-shell` arg switched from single-quoted to double-quoted so tmux processes `#{...}` substitutions. Single quotes would have passed the literal text through.

Defensive fallback: when mouse coords are empty (e.g. someone invokes show-menu --position=mouse from a non-mouse context), buildDisplayMenuArgs uses "S" instead of crashing — menu appears adjacent to status line rather than at top-left.

Verification deferred to land+apply: prefix+e centered (unchanged), right-click on row-0 status-right should anchor at the cursor.