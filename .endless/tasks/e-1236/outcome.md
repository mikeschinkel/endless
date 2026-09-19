Shipped: `endless-tmux apply` (Go binary) configures a centered second status row + prefix+e hotkey menu + right-click menus on row-0 status-right. `endless-tmux status-line` printer renders bold-yellow [E-NNNN] · project · status with a pane→session fallback so all panes in the same tmux session show the task. `endless-tmux active-id` plumbing supports the menu actions.

Smoke-tested end-to-end in Mike's tmux:
- Status row 2 visible and updating
- prefix+e → menu works
- Right-click on status-right → menu works
- Detail (d) → less popup with task detail
- Task tree (t) → less popup with tree
- Refresh (r) → immediate redraw
- Mark verify (v) — not tested (would clobber this task's status)

Discovered during implementation:
1. tmux does NOT propagate $TMUX_PANE to #() substitutions or to run-shell-from-menu contexts. Worked around by passing --pane=#{pane_id} via tmux's own format substitution.
2. Pane-only lookup is too narrow; added session-scoped fallback via `tmux list-panes -s -t <pane>` so non-Claude panes don't blank the bar.
3. Bold-yellow palette chosen to match Mike's status-right "Help" marker; works against bg=colour18 dark blue.

Post-merge actions required (documented in plan text):
1. `just install` from main checkout to symlink `bin/endless-tmux` into `/usr/local/bin/`.
2. `endless tmux apply` to re-issue tmux config with the new global path (currently embeds the worktree path; will go dark when worktree is removed).
3. Optional: remove worktree per memory `feedback_remove_worktrees_after_merge`.

Known gaps (deferred to child tasks if pursued):
- Permanent install (companion file at ~/.config/endless/tmux-status.conf + setup tmux-status verb)
- Configurable color/format
- Hotkey-collision detection on apply
- Per-region mouse events on row 1 (vs. anchored to row 0 status-right)