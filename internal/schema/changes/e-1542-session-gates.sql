-- E-1542: create gate_kinds + a fresh session_gates table for the
-- pause-on-revisit hook. The legacy pivot-gate session_gates was dropped by
-- E-1582 (e-1582-drop-session-gates.sql); this reuses the freed name with a
-- generic-from-the-start shape.
--
-- gate_kinds mirrors the GateKind Go enum (ED-1506: const-in-code is the source
-- of truth, the table exists for FK enforcement + queryability). The startup
-- integrity check (monitor.DB -> gatekind.VerifyIntegrity) fails closed on drift.
--
-- session_gates uses a kind_id discriminator + named per-kind subject columns
-- (polymorphic subject_id was rejected: it loses FK ON DELETE CASCADE). The
-- 'revisit' kind sets epic_id; future kinds add their own subject columns and a
-- gate_kinds row. An open row (cleared_at IS NULL) is a pending prompt for a
-- (session_id, kind_id) pair; at most one open row per pair is maintained by the
-- partial index plus application-level supersede on insert.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time, against the populated real DB. The sandbox and tests
-- build from schema.sql (which also declares these tables) and never apply
-- change files.

CREATE TABLE IF NOT EXISTS gate_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO gate_kinds (id, slug, label) VALUES
    (1, 'revisit', 'Revisit');

CREATE TABLE IF NOT EXISTS session_gates (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind_id INTEGER NOT NULL REFERENCES gate_kinds(id),
    epic_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT
);

CREATE INDEX IF NOT EXISTS session_gates_open
    ON session_gates(session_id, kind_id) WHERE cleared_at IS NULL;
