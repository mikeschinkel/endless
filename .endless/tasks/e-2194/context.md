Monitors started before E-2193 never pick up a new install. A monitor whose
restart fails (WARN-0019) stops firing jobs until it is restarted by hand.
Either way the only fix today is to restart each pane by hand.

Nothing Endless records says which pane holds a `session monitor`. Monitor
panes are not sessions, so `session list --json` has no row for them, and
session-monitor panes carry no tmux tag. (The project monitor's tmux session
does, `@endless_monitor`.) Finding them currently means walking the process
tree with pgrep/ps, which is a workaround for missing data.
