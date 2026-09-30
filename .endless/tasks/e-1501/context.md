spawn's _check_task_ownership (task_cmd.py) only consults active_task_id, names the owning tmux pane without a paste-ready switch command, and silently reclaims dead-pane owners.
