After a tmux crash, the tmux plugins restore each window's name, layout and
panes, but not the Claude session or the session monitor running in them.
Recovering today means going window by window: kill the extra panes, then run
`endless session resume <task_id>` (which lays out the monitor itself). In some
windows `resume` resolves a different task than the window's, and
`session goto <task> --resume` is needed instead. No pattern for which windows
has been found yet.

`session resume` assumes a single-pane window and refuses a restored window
unless given `--rebind --no-sibling-panes`, because tmux-resurrect does not
restore `@endless_*` window options. A restored window's name is the task id
shown on its tab.
