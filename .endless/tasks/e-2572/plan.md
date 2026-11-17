# Research request: which tmux server each endless command runs on

## The rule this research applies

Every endless command that touches tmux runs against Endless's named task-session
server (`endless-tasks`) and/or its named console server (`endless-console`),
reached by socket path, with the names and socket directory taken from the
`tmux.*` settings so that tests substitute their own servers. Any use of the
caller's own tmux server (the one `$TMUX` names) is a bug. (Mike, E-2572
planning.) The research does not reopen this rule; it finds every place the
code departs from it and makes each departure a filed task.

Already landed and not reopened: `task spawn`, `task claim`, `task prime`,
`session goto`, `session back`, both `session resume` paths and `endless-go
spawn-layout` address the task-session server (E-2519 and earlier).

## Scope

1. **Inventory.** Every tmux interaction in Python (`src/endless/`) and Go
   (`internal/`, `cmd/endless-go`): each `tmux` subprocess, each read of
   `$TMUX` or `$TMUX_PANE`, each caller of `monitor.TmuxServerUUID()`. Start
   from the survey in this task's analysis; it is a starting point, not the
   exhaustive list. For each site record: the command the user types, the
   file and function, which server it reaches today, and which it should
   reach: task-session server, console server, both, or none.
2. **Entry points that run with no tmux server.** `endless start` (needs the
   tmux binary, not a server), `project register`, `setup`, the verify runner
   (`internal/verifycmd`), `endless jobs run` and the job loops the monitors
   drive, the Go and Python test suites, `endless-go event emit`, and the
   Claude hooks for a Claude running outside tmux. Validate each one by
   running it with both Endless servers stopped and `$TMUX` unset. Validate
   that every other tmux-touching command refuses with "Endless isn't
   running" and does nothing.
3. **Claude hook pane binding.** A Claude Code hook (SessionStart and the
   rest, `internal/hookcmd/claude.go` through `monitor.TouchSession`) records
   the Claude's pane and the tmux server that pane is on. Today it records
   whichever server it runs in, and stamps `@server_uuid` on it, including
   the console and the user's default server. Decide what the hook records
   when the pane is not on the task-session server, what such a session reads
   (`unbound`, as a Claude outside tmux does, or something else), and what
   `session goto`, list-live and companion matching do with it. File the
   resulting task.
4. **Headless children.** `claude -p` children (verb-check, recap,
   `internal_claude.py`) inherit `$TMUX_PANE`; confirm `ENDLESS_NO_HOOKS`
   covers every path (ED-1471) and that none of them can bind a pane.
5. **The user's default tmux server.** ED-1607 rejected installing anything
   on it. Find every path that can stamp `@server_uuid` or window options
   there, including `monitor.TmuxServerUUID()` with `$TMUX` unset while a
   default server runs, and `hook prompt` (`monitor.GetTmuxContext`).
6. **Test isolation.** List every seam that lets a test substitute a server:
   Go `internal/tmuxtest.Private`/`Server`, `tmuxServerUUIDFn`,
   `tasksServerUUIDFn`, `tmuxSocketPathFn`, `endlessServerDown`; Python
   `tests/conftest.py` env scrubbing, the `tmux_cmd.tasks_server` stub and
   the isolated `tmux.socket_dir`; the verify runner's private
   `TMUX_TMPDIR`. Show that no test, verify run or job run can reach the real
   `endless-tasks`, `endless-console` or the user's default server, and name
   every code path that bypasses the seams (the bare `display-message
   #{socket_path}` in `monitor/liveness.go:observeTmuxServers` and
   `tmuxservers/terminal.go` are two candidates). Where a seam is missing,
   file a task.

## Method

Survey first, then measure. On this task's branch, add a throwaway guard:
every path that execs `tmux` fails loudly unless it addresses one of the two
named sockets (or the test seam's substitute). Run the Go suite, the Python
suite, the verify runner and `endless jobs run` against it, and record each
failure as a site in the inventory. The guard is not landed; the task's
commits hold it only until the inventory is complete.

**Sandbox.** Every run uses the worktree's sandbox database, an isolated
`tmux.socket_dir` and a private `TMUX_TMPDIR`, placed per ED-1627.
Never `endless start` or `stop` against the real Endless socket directory (the `tmux.socket_dir` default), never pass
`--db main` to anything that writes, never attach to or stamp the user's
default server. Check `tmux -S` paths in the guard's log before each suite
run. Mike: "you have to be VERY careful not to accidentally break out of the
sandbox."

## Inputs

- This task's analysis: the planning survey of tmux sites, grouped by how
  each reaches tmux.
- `docs/guide/reference.md`, "Endless's tmux servers"; `docs/guide/sessions.md`,
  "A running session is never resumed".
- Decisions: ED-1500 (bare invocations write under the nil UUID), ED-1607
  (single `tmux -L endless` server; obsolete, see deliverables), ED-1619
  (liveness answers), ED-1627 (socket directories under `/tmp`), ED-1471
  (headless children), ED-1523 (one pane-to-task resolver).
- E-2519's outcome and its landed code.
- Tasks already filed from the planning survey: E-2644 (event resolution
  scopes panes to the ambient server), E-2645 (`resume --tmux-session` runs
  with no socket), E-2646 (auto-spawn checks clients on the runner's server),
  E-2647 (companion resolvers send a console pane id to the tasks server).

## Deliverables

1. `.endless/tasks/e-2572/server-map.md`: one table, a row per command or
   entry point: the server(s) it runs on, how it addresses them today, how it
   must, and the task that fixes it. Plus the reasoning for scope items 3, 5
   and 6.
2. One bugfix task per departure from the rule, `--relates-to E-2572`, filed
   as found, not at the end. A departure E-2519 introduced is also
   `--cleans-up E-2519`.
3. A proposed decision (`endless decision add`) naming the two servers,
   `endless-tasks` and `endless-console`, reached by socket path, as the only
   servers any endless command addresses, with the test-substitution rule;
   then `endless decision supersede ED-1607` in its favor. ED-1500 reviewed:
   keep, or propose its replacement. Decisions stay proposed for Mike.
4. `outcome`: the conclusions, the list of filed tasks, the decision ids and a
   link to `server-map.md`. The research may recommend, in the outcome, a
   machine-readable server table in the code that the CLI enforces and the
   docs are generated from; it does not build one.
5. Status `unreviewed` with the outcome set; Mike completes it.
