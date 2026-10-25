-- E-2428: add tmux_server_aliases — earlier @server_uuid values of a running
-- tmux server, so a session bound before a re-stamp still reads live.
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

CREATE TABLE IF NOT EXISTS tmux_server_aliases (
    alias_uuid TEXT PRIMARY KEY,
    server_uuid TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);
