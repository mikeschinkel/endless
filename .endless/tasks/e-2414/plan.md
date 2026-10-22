# Show and set a task's steward session, and retire pins

## The steward task
- Stewardship is stored as a TASK, not a session: E-N's steward task E-M. The steward session is resolved from it at read time, so stewardship travels with E-M if the session holding E-M changes (until session instances exist, a task's session can change, which would orphan a stored session).
- A table holding at most one row per task, (task_id, steward_task_id, set_at), written only by a new event kind, `task.steward_set`, whose payload carries the task and either a steward task or none. Its executor upserts the row, or deletes it for none. Add it to `schema.sql` with a migration.
- `endless task update E-N --steward <E-M|none>`:
  - `E-M` is stored whether or not a session holds it now.
  - An `ES-N` is refused, naming that session's claimed task to use instead, or saying it holds none.
  - `E-N` itself is refused: a task's claiming session can never steward it (ED-1605).
  - Setting E-M clears any hide of E-N by a session claiming E-M.
  - `none` deletes the row.
- The board of a session claiming E-M lists every task whose steward task is E-M, so the task appears there without a touch, and still does after E-M's session changes.

## Working out the steward
- One Go function in `internal/monitor`, shared by the boards (`session_stewardship.go`) and the commands below: with a steward task E-M, the present session that claimed E-M (only one session can claim a task), unless it has hidden the task; with none, the existing rules (filer, else the only editor, claimer and hiders not counted, two at the deciding step is ambiguous).
- It is the same answer whichever session is looking.
- Exposed to Python through an `endless-go` subcommand, so Python reads no SQLite.

## Commands that show it
- `endless task steward E-N`: prints `ES-N` alone on stdout. With no steward it prints nothing on stdout, says so on stderr, and exits non-zero; when ambiguous it does the same and names the candidates.
- `endless session goto --steward E-N`: goes to that session, refusing as `task steward` does when there is none.
- `endless task show E-N`: a `Steward:` line, for example `Steward: ES-1352 (E-2300)`, `Steward: ES-1352 (E-2300), chosen` from a steward task, a note when the steward task has no present session, `Steward: ambiguous (ES-1, ES-2)` or `Steward: none`.

## Retiring pins (ED-1633)
- A row whose session touched the task is never marked stewarded-elsewhere.
- `session unhide --task` on a task that is not hidden refuses, and names `endless session touch E-N`.
- Drop `session_pinned_tasks` (migration), the pin code in `session_task_visibility.go`, `SessionPinnedTasks`, the `Pinned` row field and the `pinned` JSON field.
- Rewrite the pin paragraph in `docs/guide/sessions.md`, and document `--steward`, `task steward` and `session goto --steward` in the guide.

## Order
E-2291 has landed (filed, touched, session touch). E-2405 adds a spawned relation that counts as filing; whichever of E-2405 and E-2414 lands second merges with the other in `session_stewardship.go`.
