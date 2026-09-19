Shipped contextual hints on the status row for three cases:

1. PaneStatusActive — active task in window: render full `[E-NNN] · project · type · phase · tier · status` (unchanged from E-1246).
2. PaneStatusNoTask — sessions exist in window but no active task: render `claim a task ▸  endless task claim <id>` in italics.
3. PaneStatusClaudeNoSession — focused pane runs Claude, no Endless session for any pane in window: render `no Endless session ▸  endless setup claude-hook` in italics.
4. PaneStatusNone — none of the above: dim placeholder dot (unchanged).

New monitor.GetPaneStatus / PaneStatus types in `internal/monitor/tmux_lookup.go`. Existing GetActiveTaskForPane unchanged — still used by `active-id` and `show-menu`.

Two new internal helpers:
- `anySessionForPanes`: distinguishes "session exists for these panes" from the existing "session with active_task_id exists" check.
- `paneIsRunningClaude`: `tmux display-message -p -t <pane> #{pane_current_command}` to detect Claude in the focused pane.

Hint styling uses theme-default fg with italics so it reads against any status-style background.

Tests: 287 Python + Go monitor package pass. Synthetic verification of active and none states; the two hint states verified by code-walk since dev DB historical data makes them hard to reproduce on a real pane.

Verification deferred to land+install. On window 6 (or any window with sessions but no active task): second status row should now display the `claim a task` hint instead of the dot.