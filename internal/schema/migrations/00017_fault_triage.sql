-- E-2272: add error_triage and fault_triage_projects — the fault-triage job's
-- per-incident routing state and its per-project opt-in watermark.
--
-- The rationale lives beside the tables in internal/schema/schema.sql. The
-- statements below must stay byte-comparable with the ones there:
-- migrate_test.go's TestMigrate_MatchesSchemaSQL compares sqlite_master DDL
-- between a migrated database and a schema.sql one.
--
-- IF NOT EXISTS for 00013's reason: schema.sql is exec'd directly by tests, so
-- a database can already hold these tables before goose replays this step.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

CREATE TABLE IF NOT EXISTS error_triage (
    error_id            INTEGER PRIMARY KEY REFERENCES errors(id) ON DELETE CASCADE,
    state               TEXT NOT NULL,
    session_id          INTEGER,
    delivered_at        TEXT,
    fix_task_id         INTEGER,
    accepted_session_id INTEGER,
    accepted_at         TEXT,
    declined_session_id INTEGER,
    declined_at         TEXT,
    decline_reason      TEXT,
    escalated_at        TEXT,
    note                TEXT,
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_error_triage_fix_task
    ON error_triage(fix_task_id);

CREATE TABLE IF NOT EXISTS fault_triage_projects (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    enabled_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
