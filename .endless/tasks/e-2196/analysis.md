## Decided (Mike)

- Per window in scope: read the task id from the window name (an optional
  leading `*` is the user's attention marker), kill every pane but one, then
  resume that task in the remaining pane.
- A window already running Claude: skip it.
- A window whose name is not a task id: skip it.
- Which pane to keep: any, as long as it is at a shell prompt.
- Scope: `--tmux-session` (the current tmux session), `--tmux-session=<name>`,
  `--all-tmux-sessions`. Chosen over `--all`, which on `session resume` could
  read as "every Claude session".
- Preview: `--dry-run`.
- Settled tasks resume without changing status. This is crash recovery, and
  most sessions stay open after their task is done, to track tasks filed
  while working on it.
- A failure in one window is reported in that window's pane; the run
  continues.
- The window NAME is authoritative. A pane restored in the wrong directory is
  corrected (resume changes into the task's worktree), not refused.
- A dropped worktree is rebuilt with `--review` automatically.
- The resume is typed into the kept pane with `send-keys`.
- New logic goes in Go where reasonably possible (E-1063 is spawned but not
  started).

## Observed after the 2026-09-29 crash

Restored windows had 3 panes, all at zsh: one in the task's worktree, the
others in the main checkout or in another task's worktree (`e-2157`). Some
windows came back as a single pane in the main checkout; three of those tasks
had had their worktrees dropped after landing.
