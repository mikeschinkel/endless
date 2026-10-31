# Resolve a named session or task in any project

## Change

Two parts, one cause: a session's project decides where it can be found, and
that project is wrong for any session working another project's task.

### 1. Lookups by id ignore the cwd's project

- `endless-go session-query list-live` gains `--all-projects`, which drops the
  `s.project_id` filter in `monitor.ListLiveSessions`. `--project-root` keeps
  its meaning for listings.
- `session_cmd._live_sessions` takes an all-projects mode. Every caller that
  resolves one named session or task uses it: `session goto`, `show`, `cd`,
  `use`, `back`, `_resume_in_background`, `show_history`, and
  `_live_window_claiming` (the `--rebind` guard). So does finding the current
  window's session when no id is given (`_resolve_companion`), since a pane
  is as specific as an id.
- Each row's `worktree_path` is computed from the session's own project path,
  not the cwd's. `list-live` rows carry the project's path for this.
- `goto` and the background resume act in the session's own project and
  worktree.

### 2. A session's project is its task's project

- `bindSessionTask` (task.claimed, task.bound) also sets `sessions.project_id`
  to the claimed task's `project_id`.
- `monitor.BindSessionToTask` (the hook's auto-bind from a worktree) takes the
  project from the task, not from its caller.
- A session with no task keeps the project it was created with. Releasing a
  task leaves the session's project unchanged.
- A schema migration re-stamps every existing session that has a `task_id`
  with that task's project. Session rows are written directly, not rebuilt
  from the ledger, so nothing else would fix them.
- The project a session started in is not stored anywhere new: session_tasks
  already records which tasks it touched.

## Verify

- From go-cfgstore, `session goto`, `show`, `cd` on a live session recorded
  under another project find it.
- Claiming a go-cfgstore task from a session started in endless leaves
  `sessions.project_id` at go-cfgstore, and the next hook event does not move
  it back.
- `session list` in a project still shows only that project's sessions, now
  including sessions working its tasks.
