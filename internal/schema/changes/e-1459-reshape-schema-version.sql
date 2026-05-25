-- E-1459: reshape _schema_version from the old V-framework shape
-- (version INTEGER PK, name, applied_at, runner) to the change-file shape
-- (name TEXT PK, applied_at). Backfills the 12 historical version rows to
-- vNN-slug names, preserving applied_at. This is the proof-of-concept that
-- the schema.sql + changes/ model works on real data.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and inserts this change's own _schema_version marker after these statements,
-- so the file itself only reshapes + backfills. Not idempotent on its own; the
-- marker gates re-runs. On a fresh DB (where schema.sql already created the new
-- shape) the SELECT copies zero rows and this is a harmless no-op rebuild.

CREATE TABLE _schema_version_new (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

INSERT INTO _schema_version_new (name, applied_at)
SELECT
    CASE version
        WHEN 1  THEN 'v01-legacy-plan-task-base-tables'
        WHEN 2  THEN 'v02-drop-dead-tables-rename-tier'
        WHEN 3  THEN 'v03-session-conversation-history'
        WHEN 4  THEN 'v04-task-files-suggestions'
        WHEN 5  THEN 'v05-pre-framework-stopgap'
        WHEN 6  THEN 'v06-session-gates'
        WHEN 7  THEN 'v07-session-statuses'
        WHEN 8  THEN 'v08-session-statuses-consolidate'
        WHEN 9  THEN 'v09-session-tasks'
        WHEN 10 THEN 'v10-task-landings'
        WHEN 11 THEN 'v11-session-tasks-id-pk'
        WHEN 12 THEN 'v12-project-next-tables'
        ELSE 'v' || printf('%02d', version) || '-legacy'
    END,
    applied_at
FROM _schema_version;

DROP TABLE _schema_version;

ALTER TABLE _schema_version_new RENAME TO _schema_version;
