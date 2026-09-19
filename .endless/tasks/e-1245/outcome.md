Fixed: `internal/monitor/tmux_lookup.go` fallback narrowed from session-scoped (`tmux list-panes -s -t`) to window-scoped (`tmux list-panes -t`). Function renamed `listPanesInSameSession` → `listPanesInSameWindow`.

Synthetic test in dev tmux (multiple windows each with their own Claude session):
- pane in window 5 → E-1245
- pane in window 4 → E-1203
- pane in window 1 → E-1135

Different windows now resolve to different tasks as expected.

Post-merge action: `just install` then `endless tmux apply` (same dance as E-1236) so /usr/local/bin/endless-tmux is updated and the running tmux invokes the fixed binary.