Three ways to isolate were weighed (asked and answered in the planning
conversation):
- A: write `tmux.socket_dir` into each test's isolated config. Chosen.
- B: a new `ENDLESS_TMUX_SOCKET_DIR` environment variable. Rejected: it adds a
  second user-visible way to set the same thing, and a process that does not
  inherit it silently falls back to the config.
- C: derive the default socket directory from `TMUX_TMPDIR`. Rejected: it
  changes the product, and a process started without `TMUX_TMPDIR` would look
  in a different directory than the servers live in.

A uses the setting the product already reads, so nothing changes outside the
test suite. Its weak point is a test that replaces `config.json` and drops the
key. Plan item P3 covers that.

Go tests are left out because no Go test reaches `tmuxservers.RunningTasks`
or `tmuxservers.Resolve`. They also never see `conftest.py`.

The directory name `endless-test-tmux` was the user's choice.
