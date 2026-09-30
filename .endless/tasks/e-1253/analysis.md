Fix: capture `#{mouse_x}` and `#{mouse_y}` at binding time (tmux substitutes them while it still has the mouse event) and pass as numeric `--mouse-x` / `--mouse-y` flags to show-menu, which forwards them as `display-menu -x <num> -y <num>`.

Bindings need double-quoted run-shell args so tmux substitutes `#{...}`.
