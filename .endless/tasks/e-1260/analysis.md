Detection: `tmux display-message -p -t <pane> #{pane_current_path}` then match against the projects table.

Styling should be quieter than the main task display so the user knows it is a project-level fallback, not a session-active task.
