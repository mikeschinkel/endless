-- E-2421: add shipped_file_writes — what `endless project init` last wrote
-- into each project, per shipped file, so a re-run can tell an unedited copy
-- from an edited one.
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

CREATE TABLE IF NOT EXISTS shipped_file_writes (
    project_id INTEGER NOT NULL,
    path       TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    sha256     TEXT    NOT NULL,
    written_at TEXT    NOT NULL,
    PRIMARY KEY (project_id, path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
