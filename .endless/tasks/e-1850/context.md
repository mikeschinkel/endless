Using Endless productively today depends on Mike's local tmux configuration: the standard layout runs Claude Code in the left pane, 'endless session monitor' in the top-right pane, and leaves the bottom-right pane for ad-hoc endless commands.

A fresh clone of the repo does not get that config, so a new user has no working multi-pane setup and 'session monitor' has nowhere to live.

From the E-2223 brainstorm (decision ED-1607), this task now also owns:
- Endless runs its own tmux server (`tmux -L endless`), started by `endless start` (alias `endless tmux start`), with `~/.config/endless/tmux.conf` as its config.
- That config first sources the user's tmux config (`source-file -q ~/.tmux.conf`), then adds Endless's layer, including the window-visibility hooks the monitor gating task needs. Hooks defined in the file come back on every server start.
- Curating Mike's current tmux.conf with him: what belongs in Endless's layer, and what stays personal.
- The config source is version-controlled in the repo, and `endless setup` installs it.
- Today, launching Endless is piecemeal and works only on Mike's machine. This is the path for a PRODUCT user to start Endless after cloning.
