-- E-1975: grow the corpus row into a replayable unit, and add the five tables
-- the autoresearch loop turns on.
--
-- Only the ALTERs need to be here — schema.sql declares the new tables with
-- CREATE ... IF NOT EXISTS and is executed on every connection, so those appear
-- on the populated DB the moment the landed binary opens it. Columns cannot work
-- that way: ALTER TABLE ADD COLUMN is not idempotent and schema.sql's CREATE
-- TABLE IF NOT EXISTS silently no-ops against an existing table, so an added
-- column reaches a populated DB only through a change file. The new tables are
-- restated below anyway so this file alone is a complete description of what
-- E-1975 did to the schema.
--
-- NOT BACKFILLABLE, and worth saying out loud rather than leaving as an
-- inference: fetched_context records what the minimizer asked for and what came
-- back AT THE TIME. Rows written before this change ran cannot acquire it — the
-- state they would be re-fetched from has moved. They stay in the corpus as
-- fidelity/compression samples and are excluded from any replay that needs
-- context.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.

ALTER TABLE session_gates ADD COLUMN fetched_context TEXT;

ALTER TABLE session_gates ADD COLUMN variant_hash TEXT;

ALTER TABLE session_gates ADD COLUMN task_type TEXT;

ALTER TABLE session_gates ADD COLUMN bypassed INTEGER NOT NULL DEFAULT 0;

ALTER TABLE session_gates ADD COLUMN pair_id INTEGER;

ALTER TABLE session_gates ADD COLUMN pair_slot TEXT;

ALTER TABLE session_gates ADD COLUMN picked INTEGER NOT NULL DEFAULT 0;

ALTER TABLE session_gates ADD COLUMN emitted_text TEXT;

CREATE INDEX IF NOT EXISTS session_gates_corpus
    ON session_gates(kind_id, id) WHERE raw_draft IS NOT NULL;

CREATE TABLE IF NOT EXISTS report_labels (
    id         INTEGER PRIMARY KEY,
    gate_id    INTEGER NOT NULL REFERENCES session_gates(id) ON DELETE CASCADE,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    token      TEXT NOT NULL,
    span       TEXT,
    note       TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_report_labels_gate ON report_labels(gate_id);
CREATE INDEX IF NOT EXISTS idx_report_labels_token ON report_labels(token);

CREATE TABLE IF NOT EXISTS minimizer_variants (
    hash             TEXT PRIMARY KEY,
    task_type        TEXT NOT NULL DEFAULT '',
    prompt_text      TEXT NOT NULL,
    fetch_policy     TEXT NOT NULL,
    bypass_threshold INTEGER NOT NULL,
    parent_hash      TEXT,
    origin           TEXT,
    note             TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_minimizer_variants_type
    ON minimizer_variants(task_type, created_at);

CREATE TABLE IF NOT EXISTS minimizer_champions (
    task_type   TEXT PRIMARY KEY,
    hash        TEXT NOT NULL REFERENCES minimizer_variants(hash),
    promoted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    promoted_by TEXT,
    note        TEXT
);

CREATE TABLE IF NOT EXISTS report_judgments (
    id               INTEGER PRIMARY KEY,
    gate_id          INTEGER NOT NULL REFERENCES session_gates(id) ON DELETE CASCADE,
    variant_hash     TEXT,
    invariants_ok    INTEGER NOT NULL DEFAULT 1,
    invariant_detail TEXT,
    invented         INTEGER NOT NULL DEFAULT 0,
    fidelity         INTEGER,
    fidelity_detail  TEXT,
    compression      REAL,
    predicted_pick   TEXT,
    predicted_flag   INTEGER,
    actual_pick      TEXT,
    actual_flag      INTEGER,
    agreed           INTEGER,
    blind            INTEGER NOT NULL DEFAULT 1,
    judged_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE (gate_id)
);

CREATE TABLE IF NOT EXISTS minimizer_evals (
    id               INTEGER PRIMARY KEY,
    task_type        TEXT NOT NULL,
    challenger_hash  TEXT NOT NULL,
    champion_hash    TEXT NOT NULL,
    corpus_ids       TEXT NOT NULL,
    wins             INTEGER NOT NULL DEFAULT 0,
    losses           INTEGER NOT NULL DEFAULT 0,
    ties             INTEGER NOT NULL DEFAULT 0,
    vetoes           INTEGER NOT NULL DEFAULT 0,
    promoted         INTEGER NOT NULL DEFAULT 0,
    verdict          TEXT,
    detail           TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_minimizer_evals_type
    ON minimizer_evals(task_type, created_at);

CREATE TABLE IF NOT EXISTS minimizer_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
