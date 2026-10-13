# Plan — `endless tmux start`: launch Endless's own tmux servers

Decided with Mike, 2026-10-05 to 2026-10-12.

## What it starts

`endless tmux start`, alias `endless start`, starts two tmux servers of
Endless's own and a terminal window attached to each. Neither is the user's
default tmux server, which stays theirs.

- **console** — server `endless-console`, session `console`. One tmux window
  per use: a window per project monitor (named for its project; the project
  monitor above a shell prompt), and non-project windows such as the planned
  `terms`.
- **task-session server** — server `endless-tasks`, session `tasks`, where
  Claude task sessions live.

Server and session names are configurable (`tmux.console_server`,
`tmux.tasks_server`, `tmux.tasks_session`), defaulting to the names above.

## Behaviour

1. **Opening terminal windows.** A configured launch command, e.g.
   `"terminal": "ghostty -e {cmd}"` or `"wezterm start -- {cmd}"`, with a
   built-in default for terminals Endless can detect. With no command configured
   or detected, start both servers and print the two `tmux -L … attach`
   commands instead.
2. **Running it again** always opens both terminal windows, reusing servers and
   sessions that are already running.
3. **tmux configuration.** Each server loads the user's `~/.tmux.conf`, then an
   Endless config file that sets what Endless needs — window naming, the status
   line, and `@server_uuid` so session liveness can observe the server.
4. **Restoring task windows**, without tmux-resurrect or tmux-continuum:
   - While the task-session server runs, Endless records which task sessions
     have a live window there, written when that set changes rather than on
     every observation.
   - On start, for each session in the last recorded set whose window no longer
     exists, Endless recreates the task's window in `tasks` with the usual spawn
     layout and resumes Claude in it (the `session resume --tmux-session` sweep).
     A window closed before shutdown is not in the set, so it does not come
     back. Plain shell windows the user opened by hand are not restored.
   - `--no-restore` starts without restoring.
5. **The console's first windows:** a project-monitor window for the project
   enclosing the directory `endless tmux start` ran from. Run outside a project,
   the console opens with a plain shell window. (Restoring the previous console
   windows, or a configured list, may come later.)
6. **Adding a project monitor:** `endless project monitor <name> --tmux`, with
   `<name>` required, adds a window named for that project to the console —
   even when one already exists for that project. With no terminal attached to
   the console, it first starts the console server if needed and opens a
   terminal attached to it. This replaces E-2156's per-project sessions on the
   `endless` server.
7. **Spawns** — `task spawn`, `task claim`'s window, auto-spawn — open their
   windows in `tasks` on `endless-tasks` whenever that server is running;
   otherwise they behave as today.

E-2238 makes the console's shell act on the task-session server.

## Verification

On private tmux servers (short TMUX_TMPDIR), with a stub terminal command and a
stubbed Claude: start creates both servers with the configured names and loads
both config layers; with no terminal command it prints both attach commands; a
second start reuses the servers; the console opens with the cwd project's window
(a shell outside a project); `project monitor <name> --tmux` adds a window
named for the project each time and launches the console when none is
attached; spawns land in `tasks`; after killing the task-session server, start
recreates exactly the windows that were open (not one closed earlier) and
resumes them, and `--no-restore` recreates none; both servers carry
`@server_uuid` and their sessions read `live` in session liveness.
