-- E-2176: add the task_questions table.
--
-- The rationale lives beside the table in internal/schema/schema.sql. The
-- statements below must stay byte-comparable with the ones there:
-- migrate_test.go's TestMigrate_MatchesSchemaSQL compares sqlite_master DDL
-- between a migrated database and a schema.sql one.
--
-- IF NOT EXISTS is the narrow case 00002 describes, not the baseline's blanket
-- idempotence: schema.sql is still exec'd directly by tests and by anything
-- building a database from the declared shape, so a database can already hold
-- this table before goose replays this step.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

CREATE TABLE IF NOT EXISTS task_questions (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    series INTEGER NOT NULL,
    question TEXT NOT NULL,
    answer TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    answered_by TEXT,
    asked_by_session INTEGER,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_task_questions_task
    ON task_questions(task_id, series);
CREATE INDEX IF NOT EXISTS idx_task_questions_status
    ON task_questions(status);
