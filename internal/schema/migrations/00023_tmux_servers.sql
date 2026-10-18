-- E-2300: add tmux_servers — the socket each tmux server lived on, so liveness
-- can tell a server that is gone from one it merely could not reach.
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

CREATE TABLE IF NOT EXISTS tmux_servers (
    server_uuid TEXT PRIMARY KEY,
    socket_path TEXT NOT NULL,
    first_seen_at TEXT NOT NULL
);
