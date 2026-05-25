-- E-1469: drop the tasks.prompt column. The spawn handoff is now generated
-- from a template (docs/templates/handoff.md) merged with task metadata and
-- runtime context, so per-task stored prompt text no longer exists.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statement below.
-- This runs once, at land time (`just land`), against the populated real DB
-- where the column still exists. The sandbox (`endless-sandbox init --mode
-- empty`) and tests build from schema.sql, which no longer declares the column,
-- and never apply change files — so there is no "column absent" path to guard.
-- `prompt` participates in no trigger, index, view, or foreign key, so the drop
-- is clean.

ALTER TABLE tasks DROP COLUMN prompt;
