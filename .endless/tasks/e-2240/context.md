Mike's intended way to launch Endless (2026-10-04, discussed in the E-2156 / E-2238 session; an earlier discussion of it could not be found in tasks or session messages).

Today a user's Claude task sessions run on their default tmux server, alongside whatever they use tmux for themselves, and `endless project monitor --tmux` (E-2156) starts a separate server for the monitor. Taking over the default server would conflict with the user's own tmux use and configuration, so Endless should run on servers of its own:

- the console server (socket `endless-console`), session `console`: one window per use — `projects` (the project monitor above a shell prompt) now, a window listing terms planned;
- the task-session server (socket `endless-tasks`), holding the session where task sessions live — `tasks` by default (renamed from `active` for symmetry with `console`), configurable.

`endless tmux start`, alias `endless start`, starts both servers and opens two Endless-specific terminal windows, one attached to each, where the platform allows. E-2238 makes the console's shell act on the task-session server.


Socket paths (Mike, 2026-10-11, in the E-2300 planning session): Endless's servers use a fixed socket path, `tmux -S <fixed path>`, not `-L <name>`. A `-L` name resolves inside `TMUX_TMPDIR`, which varies between environments, so two shells could reach two different "endless-tasks" servers. A fixed path lets every Endless command reach the same server, and lets E-2300 judge liveness by asking that one known socket.

Scope (Mike, same session): tmux servers outside the ones Endless controls are not supported. Endless treats tmux as an enabling technology, not a foundational part of its architecture; it may later support other terminal multiplexers (Herdr, for example) or its own. Controlling the servers Endless uses is what keeps that open.

Socket directory (2026-10-12, E-2156 session): `/tmp/endless-<uid>/`, applying the short-directory reasoning agreed for E-2278's verify sockets on 2026-10-09; see the plan.
