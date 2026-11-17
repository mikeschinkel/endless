# Research request: which tmux server each endless command runs on

## The rule this research applies

Every endless command that touches tmux runs against Endless's named task-session
server (`endless-tasks`) and/or its named console server (`endless-console`),
reached by socket path, with the names and socket directory taken from the
`tmux.*` settings so that tests substitute their own servers. Any use of the
caller's own tmux server (the one `$TMUX` names) is a bug. (Mike, E-2572
planning; proposed as ED-1673, which modifies ED-1607.) The research does not
reopen this rule; it finds every place the code departs from it and makes each
departure part of a filed task.

Already landed and not reopened: `task spawn`, `task claim`, `task prime`,
`session goto`, `session back`, both `session resume` paths and `endless-go
spawn-layout` address the task-session server (E-2519 and earlier). E-2644
scoped event resolution (`session touch`, `session snapshot add`, `session
task add/delete`), the auto-spawn and triage client check, the companion
resolvers, `session resume --tmux-session | --all-tmux-sessions` and
`LiveSessionForPanes` to the task-session server; drop those sites from the
analysis's survey before starting.

## Answers from planning (Mike)

1. **Commands tmux itself runs inside an Endless server** (`tmux
   status-line`, `endless-go tmux show-menu`, `endless-go monitor-wake`,
   `endless-go spawn-launch`, `session monitor` / `project monitor` in their
   panes, and any others the inventory finds) receive their server's socket
   explicitly: Endless writes it into the command lines it generates for each
   server, and the command never reads `$TMUX`. Each server gets its own
   generated command lines; E-2652 builds on that to give the console its own
   status line.
2. **Claude hooks run only on `endless-tasks`.**
   - A Claude started on `endless-console` is blocked by its hooks: a message
     at SessionStart and a refusal of every prompt at UserPromptSubmit.
   - A Claude anywhere else (the user's own tmux servers, a plain terminal, an
     IDE terminal, the desktop app) is not blocked, and Endless's hooks do
     nothing for it: no pane recorded, nothing stamped on any server; the
     session reads `unbound`.
   - Open caveat, researched here under scope 4: an endless command run outside
     tmux or on the console that starts `claude -p` may need some hooks to run
     for that child. Find out whether any such child does.
3. **Filing.** One task per mechanism (one shared fix), under one epic. Work
   that belongs to another project gets its own tasks in that project; the
   planning check found tmux code only in Endless.
4. **The tmux guard** (Method, below) is thrown away when the inventory is
   complete.

## Scope

1. **Inventory.** Every tmux interaction in Python (`src/endless/`) and Go
   (`internal/`, `cmd/endless-go`): each `tmux` subprocess, each read of
   `$TMUX` or `$TMUX_PANE`, each caller of `monitor.TmuxServerUUID()`. Start
   from the survey in this task's analysis; it is a starting point, not the
   exhaustive list. For each site record: the command the user types, the
   file and function, which server it reaches today, which it should reach
   (task-session server, console server, both, or none), and the mechanism
   its fix shares with other sites.
2. **Entry points that run with no tmux server.** `endless start` (needs the
   tmux binary, not a server), `project register`, `setup`, the verify runner
   (`internal/verifycmd`), `endless jobs run` and the job loops the monitors
   drive, the Go and Python test suites, `endless-go event emit`, and the
   Claude hooks for a Claude running outside tmux. Validate each one by
   running it with both Endless servers stopped and `$TMUX` unset. Validate
   that every other tmux-touching command refuses with "Endless isn't
   running" and does nothing.
3. **Claude hook pane binding.** Today a Claude Code hook
   (`internal/hookcmd/claude.go` through `monitor.TouchSession`, plus `hook
   prompt` through `monitor.GetTmuxContext`) records whichever server it runs
   in and stamps `@server_uuid` and window options on it. Find every hook path
   that touches tmux and specify the change that implements answer 2: the
   console block, the no-op everywhere else, and what `session goto`,
   list-live, companion matching and the E-2519 resume refusal do with an
   `unbound` session.
4. **Headless children.** `claude -p` children (verb-check, recap,
   `internal_claude.py`, and any others) inherit `$TMUX_PANE`. Confirm
   `ENDLESS_NO_HOOKS` covers every path (ED-1471) and that none of them can
   bind a pane. Answer the caveat in answer 2: list each child, where it can be
   started from (outside tmux, the console, `endless-tasks`), and whether it
   needs any hook to run; if one does, say which and file the change.
5. **The user's own tmux servers.** ED-1607 and ED-1673 forbid changing them.
   Find every path that can stamp `@server_uuid` or window options there,
   including `monitor.TmuxServerUUID()` with `$TMUX` unset while a default
   server runs, and `hook prompt` (`monitor.GetTmuxContext`).
6. **Test isolation.** List every seam that lets a test substitute a server:
   Go `internal/tmuxtest.Private`/`Server`, `tmuxServerUUIDFn`,
   `tasksServerUUIDFn`, `tmuxSocketPathFn`, `endlessServerDown`; Python
   `tests/conftest.py` env scrubbing, the `tmux_cmd.tasks_server` stub and
   the isolated `tmux.socket_dir`; the verify runner's private
   `TMUX_TMPDIR`. Show that no test, verify run or job run can reach the real
   `endless-tasks`, `endless-console` or the user's own servers, and name
   every code path that bypasses the seams (the bare `display-message
   #{socket_path}` in `monitor/liveness.go:observeTmuxServers` and
   `tmuxservers/terminal.go` are two candidates). Where a seam is missing,
   it goes into a filed task.

## Method

Survey first, then measure. On this task's branch, put a throwaway stand-in
`tmux` script ahead of the real one on `PATH`: it logs each call's arguments
and caller, exits non-zero unless the call addresses one of the two Endless
sockets with `-S` (or the test seam's substitute), and otherwise passes the
call to the real `tmux`. Run the Go suite, the Python suite, the verify
runner and `endless jobs run` with it, and record each failure as a site in
the inventory. The stand-in is not landed (answer 4).

**Sandbox.** Every run uses the worktree's sandbox database, an isolated
`tmux.socket_dir` and a private `TMUX_TMPDIR`, placed per ED-1627.
Never `endless start` or `stop` against the real Endless socket directory (the
`tmux.socket_dir` default), never pass `--db main` to anything that writes,
never attach to or stamp the user's own tmux servers. Check `tmux -S` paths in
the stand-in's log before each suite run. Mike: "you have to be VERY careful
not to accidentally break out of the sandbox."

## Deliverable

`outcome` holds the inventory (or links a `docs/research-<date>-<slug>.md`
report holding it) and the filed epic. Under the epic, one task per
mechanism, each naming its sites; at least the hook change (scope 3), the
generated per-server command lines (answer 1), and one per seam gap
(scope 6) that the inventory confirms.

## Inputs

- This task's analysis: the planning survey of tmux sites, grouped by how
  each reaches tmux.
- ED-1607 and ED-1673 (proposed).
- `docs/guide/reference.md`, "Endless's tmux servers"; `docs/guide/sessions.md`,
  "A running session is never resumed".
