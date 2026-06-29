-- E-1682: durable session-navigation trail (manual + goto). Adds the
-- session_navigations runtime table plus its nav_via_kinds enum-mirror table,
-- so a global tmux focus-change hook can record every move between Claude
-- sessions/panes for usability analysis and lost-session recovery. This is the
-- same mutable, non-committed tier as `sessions` (NOT the committed JSONL
-- ledger): high-churn nav state must never pollute the shared multi-dev ledger.
--
-- nav_via_kinds mirrors the NavVia Go enum (internal/navvia/navvia.go) per
-- ED-1506; the VerifyIntegrity startup check fails closed on any drift between
-- the enum and this table. Seed rows are idempotent (INSERT OR IGNORE).
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statements below. Runs once,
-- at land time (`just land`), against the populated real DB. The sandbox and
-- tests build from schema.sql, which already declares the post-migration shape
-- (these same tables + seed rows), and never apply change files.

CREATE TABLE IF NOT EXISTS nav_via_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO nav_via_kinds (id, slug, label) VALUES
    (1, 'manual', 'Manual'),
    (2, 'goto',   'Goto');

CREATE TABLE IF NOT EXISTS session_navigations (
    id              INTEGER PRIMARY KEY,
    client          TEXT NOT NULL,
    project_id      INTEGER,
    from_session_id INTEGER,
    from_pane       TEXT,
    to_session_id   INTEGER,
    to_pane         TEXT NOT NULL,
    via_id          INTEGER NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (from_session_id) REFERENCES sessions(id) ON DELETE SET NULL,
    FOREIGN KEY (to_session_id) REFERENCES sessions(id) ON DELETE SET NULL,
    FOREIGN KEY (via_id) REFERENCES nav_via_kinds(id)
);

CREATE INDEX IF NOT EXISTS session_navigations_client
    ON session_navigations(client, id);
