-- E-2019: the enum mirror seeds, reconciled on every connect.
--
-- These tables are the ED-1506 SQL mirrors of Go enums. They are DATA
-- derived from code, not schema, which is why they live here and not in the
-- baseline migration: a migration runs once, and a mirror that runs once stops
-- being a mirror the moment the Go enum it mirrors is edited.
--
-- schema.Seed() execs this file after every goose run, which is the same
-- cadence schema.sql had before E-2019 -- so E-1659's self-heal survives the
-- move to goose: a row seeded under an old slug or label is reconciled by the
-- upsert on the next connect, before monitor.DB()'s VerifyIntegrity gates read
-- it. INSERT OR IGNORE could not do that; it would skip the existing id, leave
-- the stale slug, and fail the gate closed.
--
-- Statements are lifted verbatim from internal/schema/schema.sql, including
-- gate_kinds' INSERT OR IGNORE -- which deliberately does NOT reconcile, and is
-- left exactly as it was rather than quietly upgraded here.
--
-- Every statement must stay idempotent and safe to re-run against a populated
-- production database. Nothing but enum mirrors belongs in this file.

INSERT INTO process_kinds (id, slug, label) VALUES
    (1, 'tmux', 'Tmux pane'),
    (2, 'pid',  'OS process')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

INSERT INTO task_types (id, slug, label) VALUES
    (1, 'todo',       'Todo'),
    (2, 'bugfix',     'Bugfix'),
    (3, 'research',   'Research'),
    (4, 'epic',       'Epic'),
    (5, 'brainstorm', 'Brainstorm')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

INSERT OR IGNORE INTO gate_kinds (id, slug, label) VALUES
    (1, 'revisit', 'Revisit'),
    (2, 'relay', 'Relay');

INSERT INTO session_task_relations (id, slug, label) VALUES
    (1, 'claimed',    'Claimed'),
    (2, 'surfaced',   'Surfaced'),
    (3, 'revisited',  'Revisited'),
    -- E-1696. Ids are APPENDED, never renumbered: they are persisted in
    -- session_tasks.relation_id, so inserting in the middle would reclassify
    -- live rows. Prominence order is Relation.Rank() in Go, not the id.
    (4, 'referenced', 'Referenced'),
    (5, 'queued',     'Queued')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

-- E-1813: the rating-level mirrors of rating.Level, one table per axis. Ids 2
-- and 4 are deliberately unseeded; see internal/rating.
INSERT INTO complexity_levels (id, slug, label) VALUES
    (1, 'low',    'Low'),
    (3, 'medium', 'Medium'),
    (5, 'high',   'High')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

INSERT INTO risk_levels (id, slug, label) VALUES
    (1, 'low',    'Low'),
    (3, 'medium', 'Medium'),
    (5, 'high',   'High')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;
