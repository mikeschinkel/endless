-- E-2158: drop _schema_version, the bookkeeping table of the per-ticket
-- change-file mechanism.
--
-- Each row recorded one internal/schema/changes/<name> file as applied. goose
-- owns the schema now and records its steps in goose_db_version, and the change
-- files, their runner and every program that applied them are gone, so nothing
-- reads or writes this table any more.
--
-- DROP ... IF EXISTS: a database built from today's schema.sql never had it.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up
DROP TABLE IF EXISTS _schema_version;
