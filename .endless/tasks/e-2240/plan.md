# Plan — `endless tmux start`: launch Endless's own tmux servers

Decided with Mike, 2026-10-05 to 2026-10-12.

## What it starts

`endless tmux start`, alias `endless start`, starts two tmux servers of
Endless's own and a terminal window attached to each. Neither is the user's
default tmux server, which stays theirs.

- **console** — server socket `endless-console`, session `console`. One tmux window
  per use: a window per project monitor (named for its project; the project
  monitor above a shell prompt), and non-project windows such as the planned
  `terms`.
- **task-session server** — server socket `endless-tasks`, session `tasks`,
  where Claude task sessions live.

## Sockets

Every Endless tmux command addresses its server by a full socket path,
`tmux -S <path>`, never `-L <name>` (agreed 2026-10-11). A `-L` name resolves
inside `TMUX_TMPDIR`, which differs between shells, so two shells could reach
two different servers of the same name; a full path cannot be redirected by the
environment.

- **Directory:** `/tmp/endless-<uid>/` — short and independent of `HOME`, the
  reasoning agreed for E-2278's verify sockets on 2026-10-09. `/tmp` resolves to
  `/private/tmp` on macOS, so `/private/tmp/endless-501/endless-tasks` is 37
  bytes, far inside the ~104-byte socket limit. A path under
  `~/.config/endless/` was rejected: it grows with `HOME`, and sockets are
  runtime state, not config. Not `$TMPDIR`, which on macOS is a long
  `/var/folders/…` path.
- **Ownership:** created with mode 0700; start refuses if the directory exists
  but is not a directory, is not owned by the user, or is writable by others —
  `/tmp` is shared, and a pre-created directory would hand another user the
  sockets.
- **Reboot** clears `/tmp`; the servers do not survive a reboot either, and
  restore (below) rebuilds from the database.
- **Configuration:** `tmux.socket_dir`, `tmux.console_server`,
  `tmux.tasks_server` (socket file names) and `tmux.tasks_session`, defaulting
  to the values above. `tmux.socket_dir` and both socket names are read from the
  user-level config only — a project-level value would let two projects reach
  two different servers. A verify run points `tmux.socket_dir` at its own
  directory through its temporary `HOME`'s config, so no suite can reach the
  user's servers.
- **E-2156's `project monitor --tmux`** currently uses `tmux -L endless`; this
  task replaces that server, so nothing keeps using `-L`.

## Behaviour

1. **Opening terminal windows.** A configured launch command, e.g.
   `"terminal": "ghostty -e {cmd}"` or `"wezterm start -- {cmd}"`, with a
   built-in default for terminals Endless can detect. With no command configured
   or detected, start both servers and print the two `tmux -S <path> attach`
   commands instead.
2. **Running it again** always opens both terminal windows, reusing servers and
   sessions that are already running at the configured socket paths.
3. **tmux configuration.** Each server loads the user's `~/.tmux.conf`, then an
   Endless config file that sets what Endless needs — window naming, the status
   line, and `@server_uuid` so session liveness can observe the server until
   E-2300 judges liveness by asking these known sockets directly.
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

With `tmux.socket_dir` pointed at a per-run `/tmp/endless-verify-<id>/` through
the run's temporary `HOME` config, a stub terminal command and a stubbed Claude:
both servers' sockets are at `<socket_dir>/endless-console` and
`<socket_dir>/endless-tasks` whatever `TMUX_TMPDIR` is set to; a socket directory
owned by someone else or writable by others is refused; start creates both servers with the configured names and loads
both config layers; with no terminal command it prints both attach commands; a
second start reuses the servers; the console opens with the cwd project's window
(a shell outside a project); `project monitor <name> --tmux` adds a window
named for the project each time and launches the console when none is
attached; spawns land in `tasks`; after killing the task-session server, start
recreates exactly the windows that were open (not one closed earlier) and
resumes them, and `--no-restore` recreates none; both servers carry
`@server_uuid` and their sessions read `live` in session liveness.

## Grown scope (implementation session, 2026-10-13)

Decided with Mike during implementation:

- **The restore record is a state file**, `tmux-task-windows.json` in the
  user's Endless config directory, not the database: it only remembers which
  task windows WERE open, for after the server is gone, and it is machine-local
  runtime state rather than ledger material. tmux itself answers what is open
  now. It is read before the task-session server starts, because starting it
  fires the hook that rewrites the record.
- **The verify runner keeps every suite off the user's servers.** It writes
  `{"tmux":{"socket_dir":"<run's private tmux dir>/endless"}}` into each run's
  temporary HOME and kills any servers there at teardown. `_guard.sh` accepts
  exactly that one-file config directory and still refuses anything more.

Folded in:

- Session liveness also enumerates tmux's default socket directory and
  Endless's socket directory, so sessions on Endless's servers read `live`.
- `tmux.session_name` is removed with the per-project monitor sessions it named;
  the whole `tmux` config object is read from the CLI layer only.
- Printed `tmux … -t =session` hints are single-quoted: zsh expands a bare
  `=word` to a command path, so the pasted hint failed there (the existing
  `task spawn --tmux-session` hint included).
