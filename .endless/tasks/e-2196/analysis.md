## Decided (Mike)

- Per window in scope: read the task id from the window name, kill every pane
  but one, then resume that task in the remaining pane.
- A window already running Claude: skip it.
- A window whose name is not a task id: skip it.
- Which pane to keep: any, as long as it is at a shell prompt.
- Scope: the current tmux session by default; `--tmux-session=<name>` for a
  named one; `--all-tmux-sessions` for every one.
- Preview: `--dry-run` prints what each window would get.

## Lead for the wrong-task bug

Unconfirmed: after a restore, `@endless_task_id` is missing or stale. A
resolution path that trusts that option over the window name could pick a
different task. `--all` should go by the window name.
