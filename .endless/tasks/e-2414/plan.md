# Show and set a task's steward session, and retire pins

## The stored steward
- A table holding at most one row per task, (task_id, session_id, set_at), written only by a new event kind, `task.steward_set`, whose payload carries the task and either a session or none. Its executor upserts the row, or deletes it for none. Add it to `schema.sql` with a migration.
- `endless task update E-N --steward <E-M|ES-N|none>`:
  - `E-M` picks the present session that claimed E-M. With no present session, or more than one, it refuses and names the sessions, with the `ES-N` form to use.
  - `ES-N` names the session directly.
  - The chosen session is touched for E-N (upgrade-only, so a stronger relation stays), and its hide of E-N, if any, is cleared, so the task is on its board.
  - `none` deletes the row.
- A session that claimed E-N is refused: a claiming session can never steward its own task (ED-1605).

## Working out the steward
- One Go function in `internal/monitor`, shared by the boards (`session_stewardship.go`) and the commands below: the stored steward when its session is present, has not hidden the task and has not claimed it; otherwise the existing rules (filer, else the only editor, claimer and hiders not counted, two at the deciding step is ambiguous).
- It is the same answer whichever session is looking.
- Exposed to Python through an `endless-go` subcommand, so Python reads no SQLite.

## Commands that show it
- `endless task steward E-N`: prints `ES-N` alone on stdout. With no steward it prints nothing on stdout, says so on stderr, and exits non-zero; when ambiguous it does the same and names the candidates.
- `endless session goto --steward E-N`: goes to that session, refusing as `task steward` does when there is none.
- `endless task show E-N`: a `Steward:` line, for example `Steward: ES-1352 (E-2300)`, `Steward: ES-1352 (E-2300), chosen` for a stored steward, `Steward: ambiguous (ES-1, ES-2)` or `Steward: none`.

## Retiring pins (ED-1633)
- A row whose session touched the task is never marked stewarded-elsewhere.
- `session unhide --task` on a task that is not hidden refuses, and names `endless session touch E-N`.
- Drop `session_pinned_tasks` (migration), the pin code in `session_task_visibility.go`, `SessionPinnedTasks`, the `Pinned` row field and the `pinned` JSON field.
- Rewrite the pin paragraph in `docs/guide/sessions.md`, and document `--steward`, `task steward` and `session goto --steward` in the guide.

## Order
E-2291 has landed (filed, touched, session touch). E-2405 adds a spawned relation that counts as filing; whichever of E-2405 and E-2414 lands second merges with the other in `session_stewardship.go`.
