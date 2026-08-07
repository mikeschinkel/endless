-- E-1914: add the session_hidden_tasks table — per-session display suppression
-- of a task in `session status` / `session monitor`. A row means "session S has
-- hidden task T from its own listing"; hidden_at exists so `--only-hidden` can
-- order by how long something has been suppressed.
--
-- Its own table rather than a hidden_at column on session_tasks (ED-1545,
-- revising E-1912's design): `session status` renders rows with no session_tasks
-- row at all (read-time children, dependents, upstream blockers), so a column
-- would have forced hide to fabricate a touch that `task show` would then report
-- as real.
--
-- No FKs, matching session_tasks: a hide must be able to outlive its session or
-- task rather than cascade away underneath a live listing.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statements below, so a second
-- `apply-change` is skipped outright. IF NOT EXISTS makes the SQL itself
-- idempotent as well, so applying it to a DB already built from schema.sql (which
-- declares the post-migration shape) is a no-op rather than an error. Runs once,
-- at land time; the sandbox and tests build from schema.sql and never apply
-- change files.

CREATE TABLE IF NOT EXISTS session_hidden_tasks (
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    hidden_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task_id)
);

CREATE INDEX IF NOT EXISTS idx_session_hidden_tasks_task
    ON session_hidden_tasks(task_id);
