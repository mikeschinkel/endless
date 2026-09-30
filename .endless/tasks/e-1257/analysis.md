Two states to detect: (1) pane has an Endless session but `active_task_id IS NULL` — show `claim a task →`.

(2) pane has Claude running (`pane_current_command == claude` per `tmux display-message -p -t <pane> #{pane_current_command}`) but no `sessions` row for that pane — show `register session →`.

Otherwise keep todays dim placeholder.

Both hints must be short to fit the row.
