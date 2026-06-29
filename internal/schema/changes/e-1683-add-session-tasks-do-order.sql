-- E-1683: add session_tasks.do_order — per-session implementation order for a
-- task. NULL = unordered; equal do_order across rows of the same session marks
-- those tasks parallelizable. Session-scoped (distinct from the global
-- tasks.sort_order): two sessions may order the same task differently. Set by
-- `endless session order` via the session_tasks.ordered event (replace-all).
--
-- session_tasks is a live side-effect table (E-1322): rows are produced by the
-- task-mutation executors, never replayed by rebuild-db. This column is part of
-- that same live projection — there is no ledger replay to backfill.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statement below. Runs once,
-- at land time. The sandbox and tests build from schema.sql, which already
-- declares the post-migration shape (the column is added there too), and never
-- apply change files.

ALTER TABLE session_tasks ADD COLUMN do_order INTEGER;
