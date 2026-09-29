-- E-1993: retire the `untriaged` status and the description triage behind it.
--
-- `untriaged` held a newly filed task until something judged whether its
-- description was a sufficient spec. A task now needs a plan before it can be
-- claimed or spawned, so no description ever is, and the judgment, the status
-- and the machinery that made the judgment all go.
--
-- Rows still at `untriaged` land where a task filed today would: `submitted`
-- when it carries a plan, `unplanned` otherwise. The ledger projector maps the
-- historical events the same way (internal/events/legacy_untriaged.go), so a
-- rebuild agrees with a migrated database. On Endless's own database no row is
-- `untriaged` today; this is for every other project's.
--
-- triage_claims was the per-task spend guard of the triage sweep and has no
-- reader or writer left. DROP ... IF EXISTS because schema.sql is exec'd
-- directly by tests and no longer declares it, so a database can reach this
-- step without the table. The sweep's scheduling row in `jobs` is inert once
-- its job is unregistered; it is deleted so `endless jobs list` stops showing
-- a job that no longer exists.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up
UPDATE tasks
   SET status = CASE
           WHEN EXISTS (
               SELECT 1 FROM task_content c
                WHERE c.task_id = tasks.id AND c.name = 'plan'
                  AND trim(c.content) != '')
           THEN 'submitted'
           ELSE 'unplanned'
       END
 WHERE status = 'untriaged';

DROP INDEX IF EXISTS idx_triage_claims_expires;
DROP TABLE IF EXISTS triage_claims;

DELETE FROM jobs WHERE name = 'triage-sufficiency';
