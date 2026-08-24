# Plan: rename the session's task and epic columns

A session holds at most one task and at most one epic, so `active_` names a
distinction that no longer exists. No external users, so no reason to carry the
legacy name.

Decisions are Mike's, 2026-08-13.

## Decision 1 — rename both columns, not just the task one

`sessions.active_task_id` → `sessions.task_id`
`sessions.active_epic_id` → `sessions.epic_id`

Renaming one and leaving the other inconsistent is the exact wart this task
exists to remove.

## Decision 2 — `session_statuses` moves with it

`session_statuses` carries its own `active_task_id` with the same meaning, and
the events executor clears both together on task removal. It renames too.

## Decision 3 — the diagnostic log's JSON keys rename

`user-machine.jsonl` serializes `old_active_task_id` / `new_active_task_id`.
Those become `old_task_id` / `new_task_id`.

Existing log lines keep the old keys. Nothing needs to reconcile that — the file
is read by grep and human eyes, and no code parses it. Do not write a
compatibility shim, and do not delete the file.

## Work

**Schema.** A NEW change under `internal/schema/changes/`. Historical changes
that recreate the sessions table with the old column name are NOT edited —
they must keep reproducing the schema as it stood at their point in history.
SQLite supports `ALTER TABLE ... RENAME COLUMN`, so this need not be a table
rebuild; confirm against the minimum SQLite version the project targets.

**Go.** The monitor package names the column in SQL and in struct fields
(`ActiveTaskID` on the session row, `SessionSnapshot`, `SessionTxn`'s
`OldActiveTaskID`/`NewActiveTaskID`) across its session, resume, session_focus,
tmux_lookup and db paths. `internal/events`'s task-removal executor clears both
sessions and session_statuses. `internal/hookcmd` reads it. Rename fields and
JSON tags together with the columns.

**Python.** The session and task command paths read and write it; the test
suite asserts on it directly, including at least one raw `UPDATE sessions SET
active_task_id = ...`.

**Docs.** The schema comments describing the epic/task relationship name both
columns and must be updated in step, since they are the explanation of what the
pair means.

## Verification

- `just build` and `just test` clean.
- Grep for `active_task_id` and `active_epic_id` across Go, Python and tests:
  no hits outside `internal/schema/changes/` historical files.
- A session claim, release, and task removal each still update the renamed
  columns on both `sessions` and `session_statuses`.
- A new `user-machine.jsonl` line carries `old_task_id`/`new_task_id`.
- `session status` and the tmux status row still resolve a session's task.



---

## Addendum — write-once (added from E-1952's session, 2026-08-15)

The rename's premise ("a session holds at most one task") is currently an
intention, not an invariant. Enforce it here, in the same change that renames
the column.

**`sessions.task_id` is write-once: NULL → one value, never reassigned.**

Enforce with a trigger, not by auditing writers:

```sql
CREATE TRIGGER sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
  SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END;
```

A trigger catches paths nobody enumerated. That matters concretely: the
incident below includes one reassignment whose source could not be found by
reading the code, and the trigger will name it the next time it fires.

**Evidence.** ES-1067 (uuid `70e685df-c55b-4bed-8bec-9abf359d08ba`) held, in
order: 1953 → 1973 → 1959 → 1973 → 1953. Five assignments, one session. The
last was a manual `task bind`; the fourth (back to 1973, at
`2026-08-14T02:49:24`, when E-1959 was spawned out to ES-1101 three seconds
later) has no identified source. `autoBindFromCwd` is not it — E-1856 hardened
it to fill an unbound session or re-affirm an identical binding, never to
re-point. Only ES-1067 ever touched E-1973, so this was one session
re-pointing itself, not two sessions colliding.

**Consequence, accepted deliberately.** A session that finishes its task cannot
pick up another; it ends. Release-then-bind stops being a workflow. Endless was
built before one-session-one-task was known to be the right model, so several
paths still assume a session can move on; that mobility has caused problems
with no benefit, and this tightens it.

The trigger only constrains future writes. Rows already reassigned stay as they
are — no migration.

**Sequencing:** E-1968 (movement verbs) lands with or before this trigger, or
every re-pointing verb starts aborting on contact.
