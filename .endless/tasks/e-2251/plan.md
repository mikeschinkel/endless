# Record "not a project" in the projects table, with a marker file

Direction agreed with Mike 2026-10-06; details below are unvetted and need
his review before claiming.

- `projects.status` gains non-project values (e.g. `ignored`,
  `unregistered`). A row with one of them marks its directory, and the walk
  that resolves a directory to its project stops there: reaching an ignored
  row means "not a project, do not register".
- Marker file in the directory as belt-and-suspenders: a user can mark a
  directory "don't touch this" on disk. In a git repo, Endless adds the file
  to `.git/info/exclude` so it is never committed. Registration (auto and
  explicit) refuses a directory carrying the marker, or under one.
- `live_projects` view: only real projects. Readers of `projects` switch to
  it, so an ignored directory never appears in project list, the dashboard
  or cross-project reads.
- `project unregister` / `purge` become status changes recorded through the
  event pipeline (the `project.unregistered` / `project.purged` kinds exist,
  unused), not row deletes.
- One-time migration: each config `ignore` entry becomes an ignored row;
  then the config list is retired.
- Directories already registered under an ignored path (h2: 2 tasks) — Mike
  decides what happens to each.

Open for Mike: the exact status values; the marker file's name and contents;
whether ignore matches a subtree (as the config list does today) or the
exact directory only; what happens to h2.
