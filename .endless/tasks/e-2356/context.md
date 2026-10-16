`spawn-window` used to open its window on the server named by `$TMUX`. E-2240
changed it to ask `tmuxservers.RunningTasks()` first and, whenever Endless's
task-session server is running, to target that server's socket and ignore
`$TMUX` entirely. That is the intended behaviour for a person spawning a task.

It also applies when the Python test suite runs, because the socket path comes
from the user's own config and nothing in the suite isolates it.

The visible symptom is one red test. `test_spawn_force_still_demotes_but_warns`
sets `TMUX=fake` and asserts the spawn dies downstream, which was true when the
launcher read `$TMUX`. With the task server up the launcher reaches it instead,
creates a window there, and exits 0, so `subprocess.run(check=True)` never
raises and the assertion fails.

The window it created died at once only because the `claude` binary the test
resolves does not run. The suite is nonetheless creating windows on the server
the user's real sessions live on.

It is also invisible to CI: with no task server running `RunningTasks()` returns
false, the launcher falls back to `$TMUX`, and the test passes. So the suite is
green on a machine that has never started Endless's tmux servers and red on the
machine where they are up.
