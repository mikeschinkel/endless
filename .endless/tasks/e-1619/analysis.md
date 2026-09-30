Investigation first step: tmux show -g before/after /bg, and again after claude attach, to isolate which transition clears status-format[N].

Likely fix: a hook on attach (SessionStart? PaneAttach if it exists?) that re-runs endless tmux apply.
