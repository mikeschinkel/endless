`isolated_env` in `tests/conftest.py` gives every test a private
`tmux.socket_dir`, `<tmp_path>/endless-test-tmux`. It writes
`{"tmux":{"socket_dir":"<tmp_path>/endless-test-tmux"}}` as `config.json`
under both `<isolated HOME>/.config/endless/` and
`$XDG_CONFIG_HOME/endless/`. Both are needed because go-cfgstore reads
HOME's `.config` on macOS, not `XDG_CONFIG_HOME`, the same reason
`writeEndlessTmuxConfig` in internal/verifycmd writes both. Every subprocess a
test starts, Go or Python, then looks for Endless's tmux servers in that
directory, where nothing runs unless the test starts it.

Tests that write their own `config.json` into the isolated HOME must keep the
`tmux` key. Find them with a search of `tests/` for writes to
`config.json` / `CONFIG_FILE` under HOME or XDG_CONFIG_HOME, and have each
merge into the existing file, or go through a small conftest helper that adds
the key, rather than replace it.

The `no_endless_tasks_server` fixture stays. It is no longer the isolation,
only a shortcut that saves an `endless-go tmux tasks-target` subprocess per
spawning test. Its docstring is updated to say that.

Regression tests go in a new `tests/test_tmux_test_isolation.py`, marked
`tasks_server` so the stub is off:
- With a tmux server started at `<tmp_path>/endless-test-tmux/endless-tasks`
  holding session `tasks`, the real `tmux_cmd.tasks_server()` returns that
  socket.
- With the same private server running and `TMUX=fake`, a spawn like
  `test_spawn_force_still_demotes_but_warns` opens its window only on that
  private server. The test checks the window count there; it never queries a
  server outside the test's own directory.

Servers the tests start are killed at teardown. tmux removes the socket
directory with `tmp_path`.
