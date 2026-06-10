-- E-1538: introduce the TaskType Go enum as source of truth for tasks.type
-- (per ED-1506) and the task_types SQL mirror table for FK enforcement and
-- queryability. Add tasks.type_id (INTEGER FK), backfill from the legacy
-- tasks.type values for known slugs, leave NULL where the legacy value is
-- unauthorized (E-1548 reclassifies those), then drop the legacy column.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE
-- transaction and records this change's _schema_version marker after the
-- statements below. Runs once, at land time (`just land`), against the
-- populated real DB where the legacy `type` column still exists. The
-- sandbox (`endless-sandbox init --mode empty`) and tests build from
-- schema.sql, which declares the post-migration shape directly and never
-- applies change files.
--
-- Initial seed values: task, bug, research, epic. Unauthorized legacy
-- values (`plan`, `chore`) leave their rows with type_id NULL — visible
-- via `SELECT count(*) FROM tasks WHERE type_id IS NULL` for the E-1548
-- reclassification pass. The schema declares type_id as nullable so the
-- FK does not reject the backfill on those rows.

CREATE TABLE IF NOT EXISTS task_types (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO task_types (id, slug, label) VALUES
    (1, 'task',     'Task'),
    (2, 'bug',      'Bug'),
    (3, 'research', 'Research'),
    (4, 'epic',     'Epic');

ALTER TABLE tasks ADD COLUMN type_id INTEGER REFERENCES task_types(id);

UPDATE tasks
   SET type_id = (SELECT id FROM task_types WHERE slug = tasks.type);

ALTER TABLE tasks DROP COLUMN type;
