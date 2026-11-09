-- E-2589: add transient_runs and outages.
--
-- transient_runs is the history of each run of transient failures — logged,
-- and a fault only past its source's threshold — and outages are failures a
-- provider's own status page confirmed. Neither is a fault.
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

CREATE TABLE IF NOT EXISTS transient_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source        TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    code          TEXT NOT NULL,
    threshold     INTEGER NOT NULL,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    faulted_at    TEXT,
    closed_at     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_transient_runs_open
    ON transient_runs(source, fingerprint)
 WHERE closed_at IS NULL;

CREATE TABLE IF NOT EXISTS outages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    provider        TEXT NOT NULL,
    component       TEXT NOT NULL,
    status          TEXT NOT NULL,
    opened_by       TEXT NOT NULL,
    opened_at       TEXT NOT NULL,
    checked_at      TEXT NOT NULL,
    closed_at       TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_outages_open
    ON outages(provider)
 WHERE closed_at IS NULL;
