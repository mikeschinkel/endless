Agreed with Mike 2026-10-06 while scoping E-2216 (option A, plus a marker
file as belt-and-suspenders).

How it works today:
- "Not a project" lives in the global config's `ignore` list (21 entries).
  Only `project discover` reads it (src/endless/config.py is_ignored). The
  hook's auto-registration (internal/monitor/db.go ensureAutoRegisteredProject)
  and `project register` ignore it. Evidence: `~/Projects/go-3rd-party` is on
  the list, yet `go-3rd-party/h2` was auto-registered and now holds 2 tasks.
- `project unregister` deletes the projects row (plus notes, project_deps) in
  raw SQL with no event; its only mark is `"status":"unregistered"` in the
  project's own .endless/config.json, if that file exists.
- `projects.status` already exists; every row is 'active'.
- 87 queries in 39 files read `projects`; one filters on status. There is no
  `live_projects` view (only live_tasks and task_tree exist).
