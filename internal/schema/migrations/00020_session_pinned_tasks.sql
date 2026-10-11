-- E-2293: add session_pinned_tasks — a session's explicit override that keeps a
-- task on its own `session status` listing while another session stewards it.
--
-- The rationale lives beside the table in internal/schema/schema.sql. The
-- statement below must stay byte-comparable with the one there:
-- migrate_test.go's TestMigrate_MatchesSchemaSQL compares sqlite_master DDL
-- between a migrated database and a schema.sql one.
--
-- IF NOT EXISTS for 00013's reason: schema.sql is exec'd directly by tests, so
-- a database can already hold this table before goose replays this step.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

CREATE TABLE IF NOT EXISTS session_pinned_tasks (
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    pinned_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task_id)
);
