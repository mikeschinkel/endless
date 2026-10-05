Mike's intended way to launch Endless (2026-10-04, discussed in the E-2156 / E-2238 session; an earlier discussion of it could not be found in tasks or session messages).

Today a user's Claude task sessions run on their default tmux server, alongside whatever they use tmux for themselves, and `endless project monitor --tmux` (E-2156) starts a separate server for the monitor. Taking over the default server would conflict with the user's own tmux use and configuration, so Endless should run on servers of its own:

- the console server, `tmux -L endless-console`, session `console`: one window per use — `projects` (the project monitor above a shell prompt) now, a window listing terms planned;
- the task-session server, `tmux -L endless-tasks`, holding the session where task sessions live — `active` by default, configurable.

`endless tmux start`, alias `endless start`, starts both servers and opens two Endless-specific terminal windows, one attached to each, where the platform allows. E-2238 makes the console's shell act on the task-session server.
