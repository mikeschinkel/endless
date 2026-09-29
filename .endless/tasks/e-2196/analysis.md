## Decided (Mike)

- Per window in scope: read the task id from the window name, kill every pane
  but one, then resume that task in the remaining pane.
- A window already running Claude: skip it.
- A window whose name is not a task id: skip it.
- Which pane to keep: any, as long as it is at a shell prompt. A window with no
  pane at a shell prompt is skipped, and the summary says why.
- Scope: the current tmux session by default; `--tmux-session=<name>` for a
  named one; `--all-tmux-sessions` for every one.
- Preview: `--dry-run` prints what each window would get.
- Settled tasks (assumed/confirmed/completed) resume with `--no-revisit`. This
  is crash recovery, not reopening work, and most sessions stay open after
  their task is done, to track tasks filed while working on it.
- A failed resume in one window is reported IN THAT WINDOW'S PANE, where the
  user sees it on selecting the tab, and the run continues with the next
  window. The run never stops for one window.

## Which task a window is (Mike)

The window name is authoritative. Match it to the worktree directory: the
pane's restored working directory, as tmux-resurrect puts it back. When they
agree, resume that task and rewrite the window's `@endless_*` options as
needed; tmux-resurrect does not restore them, and stale ones are the suspected
cause of the wrong-task resumes. When the window name DIFFERS from the
worktree's task id, do not resume. Write the error into that window's pane and
continue with the other windows.

Fixing the single-window wrong-task bug is part of this task: `--all` fails
wherever `resume` resolves the wrong task, so it isn't done until that's fixed.
