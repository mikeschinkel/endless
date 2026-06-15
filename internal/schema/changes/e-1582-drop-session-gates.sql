-- E-1582: drop session_gates. Layer 2 of E-1126 (substring pivot-phrase gate)
-- removed for excessive false-positive rate (28% resolved by intended verb,
-- 33% superseded, 39% abandoned over 335 rows / ~6 weeks).
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time (`just land`), against the populated real DB where
-- the table still exists. The sandbox (`endless-sandbox init`) and tests build
-- from schema.sql, which no longer declares the table, and never apply change
-- files — so there is no "table absent" path to guard.
--
-- E-1542 will CREATE a fresh session_gates with a different shape; this change
-- only drops the legacy table and does not coordinate with that work.

DROP INDEX IF EXISTS idx_session_gates_session;
DROP TABLE IF EXISTS session_gates;
