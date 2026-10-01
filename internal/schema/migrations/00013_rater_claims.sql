-- E-2203: add the rater_claims table — the rater job's per-task spend guard.
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

CREATE TABLE IF NOT EXISTS rater_claims (
    task_id INTEGER PRIMARY KEY,
    owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rater_claims_expires
    ON rater_claims(expires_at);
