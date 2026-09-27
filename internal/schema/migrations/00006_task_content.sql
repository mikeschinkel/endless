-- E-1531: add task_content, the typed prose a task carries, one row per
-- content kind — and move the plan/analysis/notes notices onto it.
--
-- This is the ADDITIVE half. The destructive half — lifting the four tasks
-- columns' values into rows and dropping the columns — is 00007, a Go step,
-- because SQLite has no DROP COLUMN IF EXISTS (see the migrations package doc).
-- The copy lives there too rather than here: on a database schema.sql built,
-- the columns never existed, and an INSERT ... SELECT naming them would fail.
--
-- tasks_notify_sessions is DROPPED AND RE-CREATED, not left alone. The old body
-- names OLD.plan / OLD.analysis / OLD.notes, and ALTER TABLE ... DROP COLUMN
-- re-parses the whole schema and refuses a column any trigger still names — so
-- 00007 could not run until this does. The new body is the old one minus those
-- three fields, which now notify through the task_content triggers below. No
-- IF NOT EXISTS on the CREATE: the DROP just made sure it does not.
--
-- The full rationale for the table and the triggers lives beside them in
-- internal/schema/schema.sql. Every statement below must stay byte-comparable
-- with its twin there: migrate_test.go's TestMigrate_MatchesSchemaSQL compares
-- sqlite_master DDL between a migrated database and a schema.sql one.
--
-- IF NOT EXISTS on the table and the content triggers is the narrow case 00002
-- describes: schema.sql is still exec'd directly by tests and by anything
-- building a database from the declared shape, so a database can already hold
-- them before goose replays this step.
--
-- No Down section: goose never rolls this set back in production, and once
-- 00007 has run the columns a Down would restore no longer hold anything.

-- +goose Up

CREATE TABLE IF NOT EXISTS task_content (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
    UNIQUE(task_id, name)
);

DROP TRIGGER IF EXISTS tasks_notify_sessions;

-- +goose StatementBegin
CREATE TRIGGER tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status      IS NOT NEW.status
  OR OLD.phase       IS NOT NEW.phase
  OR OLD.tier        IS NOT NEW.tier
  OR OLD.description IS NOT NEW.description
BEGIN
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.id,
           (SELECT json_group_object(f, json(v)) FROM (
                SELECT 'status' AS f,
                       json_object('before', OLD.status, 'after', NEW.status) AS v
                 WHERE OLD.status IS NOT NEW.status
                UNION ALL
                SELECT 'phase',
                       json_object('before', OLD.phase, 'after', NEW.phase)
                 WHERE OLD.phase IS NOT NEW.phase
                UNION ALL
                SELECT 'tier',
                       json_object('before', OLD.tier, 'after', NEW.tier)
                 WHERE OLD.tier IS NOT NEW.tier
                UNION ALL
                SELECT 'description',
                       json_object(
                           'before', CASE WHEN OLD.description IS NULL THEN NULL
                                          WHEN OLD.description = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.description IS NULL THEN NULL
                                          WHEN NEW.description = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.description IS NOT NEW.description
           )),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           NEW.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.id
       AND st.session_id IS NOT NEW.changed_by_session
       AND s.state != 'ended';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS task_content_notify_insert AFTER INSERT ON task_content
WHEN NEW.name IN ('plan', 'analysis', 'notes')
BEGIN
    UPDATE session_notices
       SET changes = CASE
               WHEN json_type(changes, '$.' || NEW.name) IS NULL
               THEN json_set(changes, '$.' || NEW.name,
                             json_object('before', NULL, 'after', '…'))
               ELSE json_set(changes, '$.' || NEW.name || '.after', '…')
           END
     WHERE task_id = NEW.task_id
       AND notified = 0
       AND changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
       AND changed_by_session IS
           (SELECT changed_by_session FROM tasks WHERE id = NEW.task_id);
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.task_id,
           json_object(NEW.name, json_object('before', NULL, 'after', '…')),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           t.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
      JOIN tasks t ON t.id = st.task_id
     WHERE st.task_id = NEW.task_id
       AND st.session_id IS NOT t.changed_by_session
       AND s.state != 'ended'
       AND NOT EXISTS (
           SELECT 1 FROM session_notices n
            WHERE n.session_id = st.session_id
              AND n.task_id = NEW.task_id
              AND n.notified = 0
              AND n.changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
              AND n.changed_by_session IS t.changed_by_session);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS task_content_notify_update AFTER UPDATE ON task_content
WHEN NEW.name IN ('plan', 'analysis', 'notes')
  AND OLD.content IS NOT NEW.content
BEGIN
    UPDATE session_notices
       SET changes = CASE
               WHEN json_type(changes, '$.' || NEW.name) IS NULL
               THEN json_set(changes, '$.' || NEW.name,
                             json_object('before', '…', 'after', '…'))
               ELSE json_set(changes, '$.' || NEW.name || '.after', '…')
           END
     WHERE task_id = NEW.task_id
       AND notified = 0
       AND changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
       AND changed_by_session IS
           (SELECT changed_by_session FROM tasks WHERE id = NEW.task_id);
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.task_id,
           json_object(NEW.name, json_object('before', '…', 'after', '…')),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           t.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
      JOIN tasks t ON t.id = st.task_id
     WHERE st.task_id = NEW.task_id
       AND st.session_id IS NOT t.changed_by_session
       AND s.state != 'ended'
       AND NOT EXISTS (
           SELECT 1 FROM session_notices n
            WHERE n.session_id = st.session_id
              AND n.task_id = NEW.task_id
              AND n.notified = 0
              AND n.changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
              AND n.changed_by_session IS t.changed_by_session);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS task_content_notify_delete AFTER DELETE ON task_content
WHEN OLD.name IN ('plan', 'analysis', 'notes')
BEGIN
    UPDATE session_notices
       SET changes = CASE
               WHEN json_type(changes, '$.' || OLD.name) IS NULL
               THEN json_set(changes, '$.' || OLD.name,
                             json_object('before', '…', 'after', NULL))
               ELSE json_set(changes, '$.' || OLD.name || '.after', NULL)
           END
     WHERE task_id = OLD.task_id
       AND notified = 0
       AND changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
       AND changed_by_session IS
           (SELECT changed_by_session FROM tasks WHERE id = OLD.task_id);
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           OLD.task_id,
           json_object(OLD.name, json_object('before', '…', 'after', NULL)),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           t.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
      JOIN tasks t ON t.id = st.task_id
     WHERE st.task_id = OLD.task_id
       AND st.session_id IS NOT t.changed_by_session
       AND s.state != 'ended'
       AND NOT EXISTS (
           SELECT 1 FROM session_notices n
            WHERE n.session_id = st.session_id
              AND n.task_id = OLD.task_id
              AND n.notified = 0
              AND n.changed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
              AND n.changed_by_session IS t.changed_by_session);
END;
-- +goose StatementEnd
