-- E-1530: sessions table cleanup. Two concerns:
--
-- 1. Drop the vestigial `status` column. It has zero readers in code and
--    isn't defined in internal/schema/schema.sql; carried over from a
--    long-ago schema iteration. `state` is the live column.
--
-- 2. Install the NULL-on-end invariant triggers (Layer B of E-1530's
--    fix). Code-level writes that mark a session ended also NULL
--    `process` (Layer A); these triggers backstop any path that forgets,
--    including future code. The invariant: a row in state='ended' has
--    process IS NULL. Required because tmux pane ids (`%N`) are reused
--    after a tmux server restart — without NULLing `process` at end-of-
--    life, lookups for the new server's panes hit ghost rows from the
--    prior server.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE
-- transaction and records this change's _schema_version marker after
-- the statements below. Runs once, at land time (`just land`), against
-- the populated real DB where the `status` column still exists. Fresh
-- DBs built from schema.sql never had the column, and pick up the
-- triggers directly from schema.sql.
--
-- SQLite 3.51 supports `ALTER TABLE DROP COLUMN`. The `status` column
-- participates in no trigger, index, view, or foreign key, so the drop
-- is clean.

ALTER TABLE sessions DROP COLUMN status;

-- One-shot cleanup: NULL `process` on every already-ended row so the new
-- invariant holds the moment the triggers go in. A residual count after
-- planning's manual UPDATE keeps reappearing as more sessions end through
-- old code paths between planning and apply-change time.
UPDATE sessions SET process = NULL WHERE state = 'ended' AND process IS NOT NULL;

CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_update
AFTER UPDATE OF state ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_insert
AFTER INSERT ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;
