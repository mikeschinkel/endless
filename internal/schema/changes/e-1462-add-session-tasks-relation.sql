-- E-1462: classify HOW a task entered a session's scope. Introduces the
-- sessiontaskrelation.Relation Go enum as source of truth (per ED-1506) and the
-- session_task_relations SQL mirror table (goal/surfaced/revisited) for FK
-- enforcement and queryability, then adds the session_tasks.relation_id FK
-- column. relation_id is set once at capture time by the task-mutation executors
-- (claim→goal, create/import→surfaced, else→revisited) and never changed on a
-- later touch (set-once). NULL = pre-E-1462 historical row.
--
-- session_tasks is a live side-effect table (E-1322): rows are produced by the
-- task-mutation executors, never replayed by rebuild-db. relation_id is part of
-- that same live projection — there is no ledger replay to backfill, so existing
-- rows keep relation_id = NULL.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statements below. Runs once,
-- at land time. The sandbox and tests build from schema.sql, which already
-- declares the post-migration shape (the table and column are added there too),
-- and never apply change files.

CREATE TABLE IF NOT EXISTS session_task_relations (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO session_task_relations (id, slug, label) VALUES
    (1, 'goal',      'Goal'),
    (2, 'surfaced',  'Surfaced'),
    (3, 'revisited', 'Revisited');

ALTER TABLE session_tasks ADD COLUMN relation_id INTEGER REFERENCES session_task_relations(id);
