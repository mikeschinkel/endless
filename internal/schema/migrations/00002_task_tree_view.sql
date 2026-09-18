-- E-2161: add the task_tree view.
--
-- Removal now RETAINS the tree edge — the two
-- `UPDATE tasks SET parent_id = NULL WHERE parent_id = ?` statements in
-- internal/events/task_removal.go are gone — so a child keeps pointing at the
-- parent it was filed under even after that parent is removed. This view is the
-- read-time half of that trade: effective_parent_id is the nearest ancestor with
-- removed = 0, so a live child of a removed parent renders under its nearest
-- live ancestor instead of vanishing behind a row live_tasks hides.
--
-- The full rationale, and the rule for which readers convert, lives beside the
-- view in internal/schema/schema.sql. The statement below must stay
-- byte-comparable with the one there: migrate_test.go's
-- TestMigrate_MatchesSchemaSQL compares sqlite_master DDL between a migrated
-- database and a schema.sql one, and SQLite stores a view's body verbatim.
--
-- IF NOT EXISTS here is NOT the baseline's blanket idempotence (which only the
-- baseline may have). It is the narrow case schema.sql creates: schema.sql is
-- still exec'd directly by tests and by anything building a database from the
-- declared shape, so a database can already hold this view before goose replays
-- this step. Creating it twice is the no-op; the version row is what records
-- that the step ran.
--
-- No Down section: dropping the view would break every reader that has since
-- been pointed at it, and goose never rolls this set back in production.

-- +goose Up

-- +goose StatementBegin
CREATE VIEW IF NOT EXISTS task_tree AS
    WITH RECURSIVE eff_anc(id, candidate_id, depth) AS (
        SELECT id, parent_id, 0 FROM tasks WHERE removed = 0
        UNION ALL
        SELECT a.id, p.parent_id, a.depth + 1
          FROM eff_anc a JOIN tasks p ON p.id = a.candidate_id
         WHERE p.removed = 1 AND a.depth < 32
    )
    SELECT t.*, e.candidate_id AS effective_parent_id
      FROM tasks t
      LEFT JOIN (
          SELECT a.id, a.candidate_id
            FROM eff_anc a JOIN tasks p ON p.id = a.candidate_id AND p.removed = 0
      ) e ON e.id = t.id
     WHERE t.removed = 0;
-- +goose StatementEnd
