SessionStart hook (cmd/endless-hook/claude.go) reads TMUX_PANE via os.Getenv and writes it to the companion file.

PaneID has json:omitempty so empty drops from JSON entirely.

Downstream: sibling detection never matches, endless task bind fails, E-1401's gate fires.

Today's evidence: TMUX_PANE='' in both Claude env and parent shell env despite TMUX being set.
