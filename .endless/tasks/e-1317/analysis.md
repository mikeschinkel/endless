Fix: add `--pane` flag to `endless session activity` that overrides TMUX_PANE for session resolution; update the menu item to pass `--pane=#{pane_id}` (tmux substitutes the focused pane at menu invocation, before the popup is spawned).

Mirrors the same workaround already used by `endless tmux status-line` and `endless tmux active-id`.
