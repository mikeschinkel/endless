-- E-2358: a session's project is its task's project.
--
-- sessions.project_id was stamped at SessionStart from the directory the
-- harness launched in, and the cwd auto-bind re-stamped it from the same
-- place. A session started in one repo to claim another repo's task was
-- therefore recorded under the first, and every lookup scoped by project —
-- `session goto`, `show`, `cd`, the claim guard — missed it while `task show`,
-- which never scoped, called it live. From E-2358 on, claiming or binding a
-- task sets the session's project to the task's (internal/events/executor.go
-- bindSessionTask, internal/monitor/session.go BindSessionToTask); this step
-- re-stamps the sessions bound before it.
--
-- Session rows are written directly by the hook, never rebuilt from the
-- ledger, so nothing else would correct them. A session with no task keeps
-- the project it was created with, as it does from here on.
--
-- Data only: no DDL changes, so TestMigrate_MatchesSchemaSQL is unaffected.
-- Safe to run again: a second pass finds every row already at its task's
-- project.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

UPDATE sessions
   SET project_id = (SELECT t.project_id FROM tasks t WHERE t.id = sessions.task_id)
 WHERE task_id IS NOT NULL
   AND EXISTS (SELECT 1 FROM tasks t
                WHERE t.id = sessions.task_id
                  AND t.project_id IS NOT sessions.project_id);
