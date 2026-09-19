# Evidence

Mike's call, 2026-08-13, during the E-1958 session: rename
`sessions.active_task_id` to `sessions.task_id`. Rationale — "active_" was
meaningful when a session might hold several tasks and one of them was the
active one. That is no longer the model: a session holds at most one task, so
the qualifier describes a distinction that no longer exists. Endless has no
external users yet, so there is no reason to carry the legacy name.

## Scope is wider than one column

The name appears across both languages and in a second table:

- Go: the monitor package's session, resume, session_focus, tmux_lookup and db
  paths all name the column in SQL and in struct fields (`ActiveTaskID`);
  `internal/events`'s task-removal executor clears it; `internal/hookcmd`
  reads it.
- Python: session and task command paths read and write it; the pytest suite
  asserts on it directly.
- `session_statuses` carries its own `active_task_id` column with the same
  meaning, and the events executor clears both together. Renaming one without
  the other trades a legacy name for an inconsistent pair.
- `internal/schema/changes/` contains a historical change that recreates the
  sessions table with the old column name. Historical changes must keep
  reproducing the schema as it was at that point — the rename is a NEW change,
  not an edit to an old one.
- `monitor.SessionSnapshot` and `monitor.SessionTxn` both carry
  `ActiveTaskID` / `OldActiveTaskID` / `NewActiveTaskID`, and the latter two are
  serialized into `user-machine.jsonl` as `old_active_task_id` /
  `new_active_task_id`. Renaming the JSON keys breaks readers of existing log
  files; decide explicitly whether the log keeps its current key names.

## Not to be confused with

`sessions.active_epic_id` is a genuinely different thing — the epic a session is
working under while `active_task_id` holds a child. Decide whether it renames
too, or whether "active_" remains correct there because a session's epic and
task are simultaneously active.
