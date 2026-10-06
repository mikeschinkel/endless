Endless has several settings that are off by default and that a project or user must opt into: `main_sync`, `auto_spawn`, `prime`, the minimizer. Nothing ever suggests them. The guide documents each one, but a user on another machine, managing a project that is not Endless, will not learn they exist unless they read the right section.

What exists today only reports problems after they happen, and each covers one slice:

- `endless errors list`: faults already recorded.
- `endless worktree check`: one worktree, at handoff.
- `endless jobs list`: how the background jobs are running.

Nothing looks at how a machine or project is set up and says what to change. Setup problems that only surface once something fails are scattered across those surfaces too: a default branch that cannot be resolved, a hook that is not executable, a main with no upstream while `main_sync` is on.

This came up when `main_sync` shipped off by default and someone asked how a user would ever find out to turn it on.
