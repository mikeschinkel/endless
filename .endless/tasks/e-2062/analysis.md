Reproduced against internal/schema/schema.sql: seed one project, one task, one session bound to it, then run rebuild-db's own DELETE statement — 'sessions.task_id is write-once'. The trigger is BEFORE UPDATE OF task_id ON sessions, aborting when OLD.task_id IS NOT NULL and NEW differs; an FK ON DELETE SET NULL is an implicit UPDATE and fires it.

Options, neither free:
- Drop the FK action (ON DELETE NO ACTION, or no FK). SQLite has no ALTER for that, so it needs a full sessions table rebuild in a change file.
- Exempt the FK-driven write. SQLite offers no way to tell an FK-driven SET NULL from a code-driven one inside a trigger body.

Reach: rebuild-db is the ONLY 'DELETE FROM tasks' in the live tree (the other is e-1378's historical change file), so nothing else trips it today. E-1967 restores ~150 destroyed bindings, which widens the set of projects that would abort, but the defect is fully present without it — this project's own database has bound sessions already.

Found while verifying E-1967's plan bullet 'the repair survives endless-go event rebuild-db'. The projector half of that claim is correct and is asserted in tests/tasks/e-1967-verify.sh: projector.go has cases for task and decision events only, so a projection never reads or writes a session binding. It is the replacement step, not the projection, that aborts.