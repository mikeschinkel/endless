Python tests can open real tmux windows on the user's Endless task-session
server.

E-2240 made the Go spawn path (`runSpawnWindow` in
`internal/spawnlaunchcmd/spawn_window.go`, through
`tmuxservers.RunningTasks()`) send every spawn to the `tasks` session whenever
that server is running and no `--tmux-session` is given. That server is
reached by socket path under `tmux.socket_dir`, which defaults to a
per-user directory under the system temp directory (`tmuxservers.DefaultDir`), outside a test's isolated HOME.

E-2240's autouse fixture `no_endless_tasks_server` in `tests/conftest.py`
stubs only the Python side (`tmux_cmd.tasks_server`). A test that reaches
`endless-go` therefore still finds the user's real server.

Seen on 2026-10-14. While the user's `tasks` server was running, E-2296's
pytest run hit `test_spawn_force_still_demotes_but_warns` in
`tests/test_task_reopen.py`. That test sets `TMUX=fake` and relies on the
spawn failing downstream. Instead, four runs opened four real `E-1611`
windows in the user's `tasks` session, each holding an esm pane on a pytest
temp directory.

The verify runner already solves this for suites (E-2240,
`writeEndlessTmuxConfig` in internal/verifycmd) by writing a private
`tmux.socket_dir` into the run's config. The Python test isolation needs the
same: a private socket directory in each test's isolated HOME config, so
neither side of the CLI can find the user's servers.
