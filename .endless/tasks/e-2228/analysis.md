Scope settled in the E-2223 brainstorm:

1. Stopgaps that ship headroom immediately:
   - Run every `git status` with `--no-optional-locks` (or `GIT_OPTIONAL_LOCKS=0`). Plain `git status` refreshes and rewrites each worktree's index, which macOS reports to `fseventsd`.
   - Skip the per-repaint tmux resize (pane-fit) when the frame height hasn't changed.
2. Profile one monitor pane: CPU, wake-ups, threads and child processes per tick, with git, tmux, the job trigger and the database each switched off in turn. Find the source of the 100–250 wake-ups per second (candidates: Bubble Tea's input reader, an animation or spinner, Go runtime timers).
3. Fix the idle baseline, so a pane with nothing to do sleeps.

Acceptance (shared with the epic): under 0.1% CPU per idle pane, and no child processes spawned on an idle tick. Mike's manual check: 30+ windows with the machine above 90% idle.
