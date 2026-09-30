tmux interprets `-y <mouse_y>` as "place menu top at this row"; when the click is near the bottom (status bar), the menu cannot extend below, and tmux auto-flips to the top rather than shifting up.
