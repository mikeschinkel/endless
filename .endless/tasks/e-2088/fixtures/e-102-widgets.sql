-- Fixture for E-2088's verification suite: one change carrying all three kinds
-- of statement a real change carries, so "runs migrations in FULL" is testable
-- rather than asserted.
--
--   DDL       — two new tables.
--   SEED DML  — the enum-mirror row. A DDL-only apply would leave a migrated
--               database missing exactly this, and the fail-closed integrity
--               gates read these rows on the next connect.
--   BACKFILL  — the UPDATE over rows the same file just inserted.
--
-- The name is in the reserved 1-199 documentation band, and the tables are
-- invented, so applying this to a throwaway database cannot be mistaken for, or
-- collide with, any change under internal/schema/changes/.

CREATE TABLE widget_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO widget_kinds (id, slug, label) VALUES
    (1, 'standard', 'Standard'),
    (2, 'bespoke',  'Bespoke');

CREATE TABLE widgets (
    id      INTEGER PRIMARY KEY,
    slug    TEXT NOT NULL,
    kind_id INTEGER REFERENCES widget_kinds(id)
);

INSERT INTO widgets (id, slug, kind_id) VALUES
    (1, 'first',  NULL),
    (2, 'second', NULL),
    (3, 'third',  NULL);

UPDATE widgets SET kind_id = 1 WHERE kind_id IS NULL;
