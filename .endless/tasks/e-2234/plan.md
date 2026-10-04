# Add focus and placement flags to `task spawn`

Mike's spec (2026-10-04). All flags pass through to `endless-go spawn-window`
(internal/spawnlaunchcmd), which builds the `tmux new-window` argv.

| Flag | Behavior | tmux mechanism (to confirm) |
|---|---|---|
| `--no-refocus` | Spawn without moving focus; the current window stays selected | `new-window -d` |
| `--to-first` | Insert before every existing window in the session (the default for manual `task spawn`) | `-b -t <session>:{start}` |
| `--to-last` | Append after every existing window | `-a -t <session>:{end}` |
| `--to-left` | Insert just before the current window | `-b -t <current window>` |
| `--to-right` | Insert just after the current window | `-a -t <current window>` |
| `--tmux-session NAME` | Spawn in the named tmux session; error if it does not exist | target `NAME`, checked with `has-session` first |

- The four `--to-*` flags are mutually exclusive; passing two is a usage error.
- `--to-left` / `--to-right` mean the current window of the target session.
  With `--tmux-session` naming another session, that is that session's active
  window.
- **The default changes to `--to-first`** for manual `task spawn` (Mike,
  2026-10-04). Today `newWindowArgs` targets `<session>:` (next free index),
  which is neither first nor reliably last: it lands in a gap when window
  numbers have holes. `--to-last` must likewise mean the true end.
- E-2125's rule stands: the target session is always explicit, never tmux's
  "current" guess. `--tmux-session` only replaces the default (the spawner's own
  session).
- **Auto-spawn placement is configurable, defaulting to `--to-last`.** E-1814's
  auto-spawn already uses `-d` and its own target-session setting; it builds on
  these flags rather than a parallel path, and adds a user config value
  (`auto_spawn.placement`: first | last | left | right, default `last`) next to
  `auto_spawn.target`. Left and right are relative to the target session's
  active window.

## Verify

Argv-builder unit tests for each flag and for the mutual-exclusion error; one
live check against a scratch tmux server (`tmux -L <name>`) asserting the new
window's index and that the previously active window is still active under
`--no-refocus`.
