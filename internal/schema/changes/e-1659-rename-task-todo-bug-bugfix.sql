-- E-1659: rename two task-type slugs (and their labels) in place.
--   task -> todo   ('task' collides with the generic term for any tree item;
--                    'todo' is an unambiguous singular for an action item)
--   bug  -> bugfix ('bug' names the problem and could imply researching it;
--                    'bugfix' commits the type to the action)
--
-- type_id is the stable identity (TaskTypeTask=1, TaskTypeBug=2 in
-- internal/tasktype/tasktype.go, the ED-1506 source of truth); the slug/label
-- are only presentation on that integer. This change renames the rows in the
-- task_types mirror; no tasks row moves type_id, so nothing else in the DB is
-- touched. Historical `task.created` / `task.fields_updated` events still carry
-- the OLD slug string and replay correctly because tasktype.Parse() accepts
-- 'task'/'bug' as aliases for the same ids — so a rebuild-db reproduces this
-- state with zero further migration.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statements below. Runs once,
-- at land time (`just land`), against the populated real DB. The sandbox and
-- tests build from schema.sql, which already declares the post-rename shape,
-- and never apply change files. The VerifyIntegrity startup check fails closed
-- if this mirror ever drifts from the Go enum's String()/Label().

UPDATE task_types SET slug = 'todo',   label = 'Todo'   WHERE id = 1;
UPDATE task_types SET slug = 'bugfix', label = 'Bugfix' WHERE id = 2;
