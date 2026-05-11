# Projects

Endless tracks multiple projects from a single global DB. Every project is a registered directory.

## Common commands

```bash
endless list                                   # all registered projects
endless status                                 # detailed status of current project
endless status --project <name>                # of a named project
```

## Registering a project

Most repos are already registered. If you `cd` into one and Endless errors with "no project for this cwd", register it:

```bash
endless register                               # register current directory
endless register --name <custom-name>          # with an explicit name
```

After registering, `endless list` should show the new project and `endless status` should work from inside the repo.

## Project name vs path

Each project has:

- A **name** (used in `--project <name>` flags).
- A **path** (the directory it was registered from).

`endless list` shows both.

## Where project state lives

For each registered project, Endless creates:

- `.endless/` directory at the repo root, holding:
  - `config.json` — project-local Endless config.
  - `db-ledger/` — write-ahead log entries; **committed to git** (E-1198).
  - `plans/E-NNNN.md` — plan files attached to tasks.
  - `plans/snapshots/` — PostToolUse hook snapshots.
  - `worktrees/<slug>/` — per-task git worktrees (gitignored — E-975).
  - `worktree.json` — current session's task→worktree mapping.

See `endless guide layout` for details.

## See also

- `endless guide layout` — file paths under `.endless/`
- `endless guide worktree` — per-task worktrees
- `endless guide sql` — read-only queries against the global DB
