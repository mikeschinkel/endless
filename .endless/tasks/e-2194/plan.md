# Restart every session monitor pane in place

## Command

`endless session monitor --restart` respawns every session-monitor pane in
scope onto the installed binary, in place (same pane, same position in the
layout).

Scope, the same three-way choice as `session resume --tmux-session`:
- default: the current tmux session;
- `--tmux-session=<name>`: a named one;
- `--all-tmux-sessions`: every tmux session.

`--dry-run` lists the panes it would restart. Outside tmux, `--restart`
without `--tmux-session=<name>` or `--all-tmux-sessions` is a usage error.

The monitor is always restarted as plain `endless session monitor`: no flags
are replayed (YAGNI; the monitor is started through `esm`, which passes none).
Project monitors are out of scope.

## Finding the panes: the monitor tags its own pane

- When `session-status --monitor` starts its loop inside tmux, it sets a PANE
  option on `$TMUX_PANE`: `@endless_session_monitor=<pid>`, the pid of the
  Go monitor process. Best-effort; failure to set it never stops the monitor.
- The loop unsets it on a clean exit (SIGINT/SIGTERM, render fatal), so a pane
  that returns to its shell after `esm` quits is no longer tagged.
- `--restart` lists panes in scope with `#{@endless_session_monitor}` and
  restarts only those whose pid is still alive and is still a
  `session-status --monitor` process. A tag naming a dead or different
  process (the monitor was SIGKILLed) is stale: clear it and skip the pane.
- A re-exec (E-2193) keeps the pid, so the tag stays valid across installs.
  Monitors started since E-2193 re-exec into this build on install and tag
  their panes at startup, so no one-time migration step is needed beyond
  restarting monitors older than E-2193 by hand.

No process-tree walking and no pane ids anywhere: the tag is the record.

## Restarting

`tmux respawn-pane -k -t <pane> <cmd>`, where `<cmd>` is the same argv
`task spawn` puts in its monitor pane (`spawnlaunchcmd.monitorCommand`, reused
rather than restated). A pane whose monitor was typed at a shell prompt
(`esm`) comes back as a monitor-only pane; quitting it then closes the pane.
The summary prints one line per pane restarted or skipped, and why.

## Where the code goes

Go, per the rule that new logic goes to Go: the tagging lives in the monitor
loop (`internal/sessionstatuscmd`, around `liveview.Loop`); the restart is a
new `endless-go` subcommand. Python gets only the Click options on
`session monitor` and a passthrough.

## Tests

- Unit: tag/untag argv; the stale-tag rule (dead pid, pid that is not a
  monitor); scope resolution (current / named / all, and the no-tmux error).
- Verify suite, against a throwaway tmux server (`tmux -L <socket>`): start a
  monitor in a pane, assert the tag; run `--restart`, assert the pane holds a
  NEW monitor pid with the tag; quit a monitor cleanly and assert the tag is
  gone; SIGKILL one and assert `--restart` clears the stale tag and skips it.

## Acceptance

- `endless session monitor --restart` in the `active` session restarts every
  monitor pane there, and nothing else.
- Panes that are not monitors, including a shell that once ran `esm`, are
  never respawned.
