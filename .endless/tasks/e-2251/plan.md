# Record "not a project" in the projects table, with a marker file

Decided with Mike 2026-10-06 (in the E-2251 session):

- One new status value: `ignored`. `unregister` and `purge` both set it
  (purge also deletes `.endless/`). No separate `unregistered`.
- Marker file: `.endless-ignore` (empty, or a free-text reason) in the
  directory. `project ignore <dir> --marker` writes it and, inside a git repo,
  adds it to `.git/info/exclude`.
- Ignore covers a subtree, and the NEAREST row/marker wins. Resolving a
  directory walks up from it; the first rung holding a projects row or a marker
  decides: an active row → that project; an ignored row or a marker → not a
  project, never auto-registered. So `~/Projects` ignored and
  `~/Projects/endless` registered coexist.
- Rule A: explicit `project register` is allowed under an ignored ancestor (it
  creates/activates the row for that exact directory). It refuses only a
  directory that itself carries the marker. Registering a directory whose own
  row is `ignored` re-activates it. h2 stays as it is.
- Full scope in this task.
- Writes go through Go but NOT the ledger: projects rows are a machine-local
  registry (paths), not ledger-projected, and a non-project directory has no
  ledger. A new `endless-go project` subcommand owns them. The declared
  `project.*` event kinds stay unused (E-2215's ground).

## Go

- `monitor`: one nearest-wins resolver (rows + marker) used by
  `ProjectIDForPath` (hook, auto-registration) and `ProjectForCwd`. An ignored
  directory yields `ErrIgnoredDirectory`; the hook and prompt hook no-op on it,
  session-query answers empty / refuses.
- `endless-go project resolve --path P...` → JSON verdict per path
  (project / ignored / none), the single implementation Python uses.
- `endless-go project set-status --path P --status ignored|active` and
  `project clear --path P` (drop an ignored, non-project row).
- Migration 00015 (Go): `live_projects` view (`status != 'ignored'`), and a
  one-time import of the `ignore` list from the `config.json` that sits beside
  the database being migrated (so only the main DB imports; sandboxes, tests
  and projection temp DBs have no sibling config). Ignored rows without a
  project are named by their stored path (`~/Projects/clients`), which can
  never collide with a real project name. Mirrored in schema.sql.
- Readers that list or look up projects switch to `live_projects`; the
  path-resolution walks keep reading `projects`.

## Python

- `config.is_ignored` / `add_ignore` call the Go resolver / set-status; the
  config `ignore` list is no longer read or written (left inert in the file).
- `register_project`: refuse a directory carrying the marker; re-activate an
  ignored row.
- `discover` and `reconcile` skip ignored directories.
- `unregister` / `purge`: status change via Go, no row deletes.
- New `project ignore [<dir>] [--marker]` (no arg lists) and
  `project unignore <dir>`.
- Readers that list projects switch to `live_projects`.
