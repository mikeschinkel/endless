-- E-1378: extract decisions from the tasks table into a dedicated decisions
-- table, and decision-sourced relations from task_deps into decision_relations.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time (`just land`), against the populated real DB where
-- tasks.type='decision' rows still exist. The sandbox (`endless-sandbox init
-- --mode empty`) and tests build from schema.sql, which declares the new
-- tables but does not perform this data movement — change files are only
-- applied at land time.
--
-- Status mapping for migrated decisions:
--   confirmed | completed | assumed -> accepted
--   needs_plan | ready              -> proposed
--   anything else                   -> accepted (best-effort fallback)
--
-- Source-table mapping for relations: rows whose source is a decision move to
-- decision_relations; task-sourced rows (incl. task->decision) stay in
-- task_deps with target_type='decision' (the column was previously hardcoded
-- to 'task'). E-1389 will later rename task_deps -> task_relations.

-- 1. Copy decision rows from tasks to decisions with status mapping.
INSERT INTO decisions
    (id, project_id, title, description, text, status, created_at, updated_at)
SELECT
    id, project_id, title, description, text,
    CASE status
        WHEN 'confirmed'  THEN 'accepted'
        WHEN 'completed'  THEN 'accepted'
        WHEN 'assumed'    THEN 'accepted'
        WHEN 'needs_plan' THEN 'proposed'
        WHEN 'ready'      THEN 'proposed'
        ELSE 'accepted'
    END,
    created_at,
    COALESCE(updated_at, '')
FROM tasks
WHERE type = 'decision';

-- 2. Move decision-sourced rows from task_deps to decision_relations.
-- Source identification: join to tasks WHERE type='decision' identifies
-- the rows pre-deletion; target_kind is derived from the target's type.
INSERT INTO decision_relations
    (source_decision_id, target_kind, target_id, relation_type, created_at)
SELECT
    td.source_id,
    CASE
        WHEN EXISTS (SELECT 1 FROM tasks t WHERE t.id = td.target_id AND t.type = 'decision')
            THEN 'decision'
        ELSE 'task'
    END,
    td.target_id,
    td.dep_type,
    td.created_at
FROM task_deps td
WHERE EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = td.source_id AND t.type = 'decision'
);

-- 3. Remove the rows we just moved out of task_deps.
DELETE FROM task_deps
WHERE EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = task_deps.source_id AND t.type = 'decision'
);

-- 4. Update task_deps rows whose target is now a decision: flip target_type
-- to 'decision'. Until this change, every task_deps row hardcoded
-- target_type='task' regardless of the target's actual type — going forward
-- the column reflects reality and the dispatchers rely on it.
UPDATE task_deps SET target_type = 'decision'
WHERE target_type != 'decision'
  AND EXISTS (
      SELECT 1 FROM tasks t WHERE t.id = task_deps.target_id AND t.type = 'decision'
  );

-- 5. Delete the migrated rows from tasks. After this, tasks holds no
-- type='decision' rows; all such rows live in decisions.
DELETE FROM tasks WHERE type = 'decision';
