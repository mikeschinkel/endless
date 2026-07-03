-- E-1719: make task_landings.branch nullable (was TEXT NOT NULL). A
-- historical/record-only landing has no surviving branch to name — the worktree
-- and its branch are long gone — so it records NULL rather than a fabricated
-- name. SQLite cannot drop a NOT NULL constraint with ALTER COLUMN, so this
-- rebuilds the table: create the new shape, copy all rows verbatim, drop the
-- old, rename, and recreate the index. Mirrors the e-1459 reshape pattern.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and inserts this change's own _schema_version marker after these statements,
-- so the file itself only rebuilds. Not idempotent on its own; the marker gates
-- re-runs. On a fresh DB (where schema.sql already created the nullable shape)
-- the SELECT copies zero-or-all rows and this is a harmless rebuild.

CREATE TABLE task_landings_new (
    id               INTEGER PRIMARY KEY,
    task_id          INTEGER NOT NULL,
    session_id       INTEGER,
    branch           TEXT,
    merge_commit_sha TEXT    NOT NULL,
    landed_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);

INSERT INTO task_landings_new (id, task_id, session_id, branch, merge_commit_sha, landed_at)
SELECT id, task_id, session_id, branch, merge_commit_sha, landed_at
FROM task_landings;

DROP TABLE task_landings;

ALTER TABLE task_landings_new RENAME TO task_landings;

CREATE INDEX IF NOT EXISTS idx_task_landings_task
    ON task_landings(task_id, landed_at DESC);
