# Rule to document

Add a short subsection to `docs/guide/orchestration.md`, under the per-task
verification suite guidance, stating the isolation contract a suite must meet.

## The rule

A verification suite may never start a tmux server, or run `endless-go`, without
isolating BOTH:

- **tmux config** — start private servers with `tmux -L <socket> -f /dev/null`,
  so the user's `~/.tmux.conf` (and any hook it installs, e.g. `session-created`
  -> `endless tmux init`) cannot fire against the throwaway server.
- **`XDG_CONFIG_HOME`** — point it at a throwaway directory for the server
  process, so every descendant (anything the suite spawns, and anything THOSE
  spawn) resolves its config dir, and therefore its database, away from
  `~/.config/endless/endless.db`.

Kill the server and remove the temp dirs in a trap on exit.

## Why the second one is load-bearing

Inside a private tmux server, `$TMUX` names a server with a handful of panes.
Any endless-go code path that decides liveness from `tmux list-panes -a` — most
importantly `monitor.ReapDeadTmuxPanes` — will conclude that every pane it does
not see is dead. Pointed at the user's real database, that nulls `process` for
every live session in the project and blanks every tmux status line on the
machine (observed 2026-08-05: 27 orphaned sessions, 59 of 61 windows blank).
Isolating the config dir means the worst such a path can do is damage a temp DB.

`-f /dev/null` alone is NOT sufficient: it only blocks config-file hooks, not
processes the suite launches itself.

## Reference implementation

`tests/tasks/e-1851-verify.sh` does both, with the reasoning inline at each
`new-session` call. Point the guide at it rather than restating the code.

## Related

- E-1898 is the product-side backstop (the reaper should refuse an implausible
  pane set instead of trusting an ambient `$TMUX`). This task is the authoring
  convention that keeps suites from tripping it in the first place.
