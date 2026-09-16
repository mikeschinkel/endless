-- E-2019: the baseline migration.
--
-- GENERATED, once, from a database built by internal/schema/schema.sql as it
-- stood when E-2019 landed — dumped from sqlite_master in creation order, so
-- what goose replays is provably the shape that code produced. Do not hand-edit
-- it and do not "refresh" it against a later schema.sql: this file is the fixed
-- point every later migration is a step away from, and rewriting it silently
-- redefines what every database already recorded at version 1 is claimed to hold.
--
-- Deliberately NOT here: schema.sql's three leading PRAGMAs (journal_mode,
-- busy_timeout, foreign_keys). They configure a connection, not a schema, every
-- opener already sets them, and journal_mode cannot change inside the
-- transaction goose wraps a migration in.
--
-- Also not here: the enum mirror seeds. They are reconciled from the Go enums on
-- every connect by schema.Seed(), not applied once — see internal/schema/seeds.sql.
--
-- Every statement carries IF NOT EXISTS, and THIS MIGRATION ALONE may. SQLite
-- does not keep that clause in sqlite_master, so the dump came back without it
-- and the generator puts it back deliberately. The one database that matters
-- already holds this shape, acquired before anything recorded versions; replaying
-- bare CREATE statements onto it would die on the first table. Idempotent here
-- means the first connect after E-2019 records version 1 and changes nothing,
-- which is verified against a snapshot of the real ledger in migrate_test.go.
--
-- A later migration must not copy this. A forward step is not idempotent, and one
-- that silently skips its own work is a migration that lies about having run.
--
-- No Down section. Rolling the baseline back means dropping every table in the
-- database, which is not a migration, it is a deletion.

-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS _schema_version (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    label TEXT,
    path TEXT NOT NULL UNIQUE,
    group_name TEXT,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    language TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'runtime',
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_id) REFERENCES projects(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    note_type TEXT NOT NULL,
    message TEXT NOT NULL,
    source TEXT,
    target_doc TEXT,
    resolved INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    resolved_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS process_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS processes (
    id            INTEGER PRIMARY KEY,
    kind_id       INTEGER NOT NULL DEFAULT 1,
    server_uuid   TEXT,
    address       TEXT NOT NULL,
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (kind_id) REFERENCES process_kinds(id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processes_identity
    ON processes (kind_id, ifnull(server_uuid, ''), address);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    task_id INTEGER,
    epic_id INTEGER,
    process_id INTEGER,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    hidden INTEGER NOT NULL DEFAULT 0,
    -- Per-turn report state (E-1953). All four are reset when the user speaks
    -- again, because a turn is exactly the span between two user prompts.
    -- last_user_prompt is staged at UserPromptSubmit so `task report` — a
    -- subprocess with no view of the turn — can copy it into the corpus row.
    -- report_bounces is the loop guard for the never-called case, which has no
    -- session_gates row to count on. report_exempt carries the `$FULL` license
    -- across the gap between UserPromptSubmit and Stop, which are separate
    -- processes and so cannot share a flag in memory. report_runs bounds the
    -- appeal at one: the first run is the report, the second is the appeal, a
    -- third is refused — an unbounded appeal is a second bite the agent will
    -- always take.
    last_user_prompt TEXT,
    report_bounces INTEGER NOT NULL DEFAULT 0,
    report_exempt INTEGER NOT NULL DEFAULT 0,
    report_runs INTEGER NOT NULL DEFAULT 0,
    UNIQUE (session_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (process_id) REFERENCES processes(id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
    SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_types (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    parent_id INTEGER,
    title TEXT NOT NULL,
    description TEXT,
    plan TEXT,
    phase TEXT NOT NULL DEFAULT 'now',
    status TEXT NOT NULL DEFAULT 'unplanned',
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    type_id INTEGER REFERENCES task_types(id),
    updated_at TEXT NOT NULL DEFAULT '',
    tier INTEGER,
    outcome TEXT,
    analysis TEXT,
    notes TEXT,
    changed_by_session INTEGER,
    removed INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES tasks(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIEW IF NOT EXISTS live_tasks AS
    SELECT * FROM tasks WHERE removed = 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_notices (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    changes TEXT NOT NULL,
    changed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    changed_by_session INTEGER,
    notified INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_session_notices_undelivered
    ON session_notices(session_id, notified);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status      IS NOT NEW.status
  OR OLD.phase       IS NOT NEW.phase
  OR OLD.tier        IS NOT NEW.tier
  OR OLD.description IS NOT NEW.description
  OR OLD.plan        IS NOT NEW.plan
  OR OLD.analysis    IS NOT NEW.analysis
  OR OLD.notes       IS NOT NEW.notes
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
                UNION ALL
                SELECT 'plan',
                       json_object(
                           'before', CASE WHEN OLD.plan IS NULL THEN NULL
                                          WHEN OLD.plan = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.plan IS NULL THEN NULL
                                          WHEN NEW.plan = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.plan IS NOT NEW.plan
                UNION ALL
                SELECT 'analysis',
                       json_object(
                           'before', CASE WHEN OLD.analysis IS NULL THEN NULL
                                          WHEN OLD.analysis = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.analysis IS NULL THEN NULL
                                          WHEN NEW.analysis = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.analysis IS NOT NEW.analysis
                UNION ALL
                SELECT 'notes',
                       json_object(
                           'before', CASE WHEN OLD.notes IS NULL THEN NULL
                                          WHEN OLD.notes = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.notes IS NULL THEN NULL
                                          WHEN NEW.notes = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.notes IS NOT NEW.notes
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
CREATE TABLE IF NOT EXISTS gate_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_gates (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind_id INTEGER NOT NULL REFERENCES gate_kinds(id),
    epic_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT,
    -- 'relay' kind (E-1901): the exact text the session owes the user as its
    -- final message, and how many times the Stop gate has bounced it. The
    -- bounce counter is the loop guard — Claude Code's stop_hook_active flag is
    -- undocumented, so blocking is capped on a value we control.
    sanctioned_text TEXT,
    bounces INTEGER NOT NULL DEFAULT 0,
    -- 'relay' kind (E-1953): the rest of the eval-corpus triple. sanctioned_text
    -- above is the minimized output; these two are the raw draft the agent
    -- submitted and the user message that prompted the turn. raw_draft is also
    -- what `task report --raw` prints, which is what lets the minimizer be
    -- aggressive at zero risk — nothing it cuts is destroyed, only hidden.
    --
    -- label / label_text arrive on the FOLLOWING turn ($CUT/$BLOAT/$WRONG/$GOOD
    -- as the first token of a user prompt), so they are nullable and written by
    -- a second statement against an already-closed row. task_id is attribution
    -- only — an id-less report is legitimate, so the corpus keys on the session.
    raw_draft TEXT,
    user_prompt TEXT,
    task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    label TEXT,
    label_text TEXT,
    -- 'relay' kind (E-1975): the rest of what a paired replay needs to re-run a
    -- turn without re-fetching live state.
    --
    -- fetched_context is the JSON record of what the minimizer's fetch policy
    -- ASKED FOR and what came back. It is what turns the corpus row from a
    -- triple into a replayable unit: a tool-using minimizer is nondeterministic
    -- in its INPUTS, so an A/B over a frozen corpus is meaningless unless replay
    -- serves context from the record instead of re-fetching state that has since
    -- moved. NOT BACKFILLABLE — rows written before E-1975 can never gain it.
    --
    -- variant_hash attributes the row to the exact (prompt, fetch policy, bypass
    -- threshold) bundle that produced it, and task_type records which per-type
    -- bucket that bundle was drawn from. Without both, a promotion cannot tell
    -- which of its own outputs it is being judged on.
    --
    -- bypassed marks a draft that skipped the minimizer because it fell under
    -- the variant's bypass threshold. The row is still corpus: "we let this one
    -- through untouched" is exactly the evidence the threshold axis is tuned on.
    --
    -- pair_id / pair_slot group the two rows of an A/B presentation. pair_id is
    -- the id of the A row (the A row points at itself), so a pair is one query.
    -- picked records the user's `$A` / `$B` — the only real counterfactual in
    -- the corpus, and the exact judgment promotion requires.
    fetched_context TEXT,
    variant_hash TEXT,
    task_type TEXT,
    bypassed INTEGER NOT NULL DEFAULT 0,
    pair_id INTEGER,
    pair_slot TEXT,
    picked INTEGER NOT NULL DEFAULT 0,
    -- What `task report` actually PRINTED, when that differs from this row's
    -- sanctioned_text. On an A/B turn the two rows each hold their own variant
    -- while the A row holds the combined presentation the agent owes the user,
    -- so the Stop gate can accept ANY OF N sanctioned texts rather than exactly
    -- one. NULL means "same as sanctioned_text", which is every ordinary turn.
    --
    -- Any-of-N is the only thing standing between the shipped shape and both
    -- later ones (an AskUserQuestion preview where the agent sends the winner,
    -- and a left/right TUI), so it is bought now while it is one comparison.
    emitted_text TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS session_gates_open
    ON session_gates(session_id, kind_id) WHERE cleared_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS session_gates_corpus
    ON session_gates(kind_id, id) WHERE raw_draft IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_deps (
    id INTEGER PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id INTEGER NOT NULL,
    target_type TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'blocks',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_type, source_id, target_type, target_id, dep_type)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS decisions (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    text TEXT,
    status TEXT NOT NULL DEFAULT 'proposed',
    origin_task_id INTEGER,
    origin_session_id INTEGER,
    notes TEXT,
    rejection_reason TEXT,
    -- Why an accepted decision stopped applying, with no replacement (E-1920).
    -- Separate from rejection_reason rather than a shared `end_reason`: the two
    -- answer different questions ("why we said no" vs "what went away"), a row
    -- can only be in one of the two states, and merging them would have meant
    -- rewriting existing rejected rows for no gain.
    obsolete_reason TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (origin_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (origin_session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS decisions_updated_at AFTER UPDATE ON decisions
BEGIN
    UPDATE decisions SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS decision_relations (
    id INTEGER PRIMARY KEY,
    source_decision_id INTEGER NOT NULL,
    target_kind TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    relation_type TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_decision_id, target_kind, target_id, relation_type),
    FOREIGN KEY (source_decision_id) REFERENCES decisions(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_decision_relations_target
    ON decision_relations(target_kind, target_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS activity (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    source TEXT NOT NULL,
    working_dir TEXT,
    session_context TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_messages (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_name TEXT,
    message_uuid TEXT UNIQUE,
    created_at TEXT NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_session_messages_session
    ON session_messages(session_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIRTUAL TABLE IF NOT EXISTS session_messages_fts USING fts5(
    content,
    content=session_messages,
    content_rowid=id
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS session_messages_ai AFTER INSERT ON session_messages BEGIN
    INSERT INTO session_messages_fts(rowid, content) VALUES (new.id, new.content);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS session_messages_ad AFTER DELETE ON session_messages BEGIN
    INSERT INTO session_messages_fts(session_messages_fts, rowid, content) VALUES('delete', old.id, old.content);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_landings (
    id                INTEGER PRIMARY KEY,
    task_id           INTEGER NOT NULL,
    session_id        INTEGER,
    base_branch       TEXT,
    merge_commit_sha  TEXT    NOT NULL,
    landed_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    landed_by_harness TEXT,
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_task_landings_task
    ON task_landings(task_id, landed_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS task_landings_notify_sessions
AFTER INSERT ON task_landings
BEGIN
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.task_id,
           json_object('landed', json(json_object(
               'before', NULL,
               'after', CASE
                            WHEN NEW.base_branch IS NULL OR NEW.base_branch = ''
                            THEN substr(NEW.merge_commit_sha, 1, 7)
                            ELSE NEW.base_branch || '@' ||
                                 substr(NEW.merge_commit_sha, 1, 7)
                        END))),
           NEW.landed_at,
           CASE WHEN NEW.landed_by_harness IS NOT NULL THEN NEW.session_id END
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.task_id
       AND s.state != 'ended'
       AND NOT (NEW.landed_by_harness IS NOT NULL
                AND st.session_id IS NEW.session_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_statuses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER,
    task_id INTEGER,
    created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    headline TEXT,
    summary TEXT,
    tasks TEXT,
    decisions TEXT,
    commits TEXT,
    memory TEXT,
    notes TEXT,
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS session_statuses_session_recent_idx
    ON session_statuses (session_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_task_relations (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_tasks (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    do_order INTEGER,
    relation_id INTEGER REFERENCES session_task_relations(id),
    UNIQUE(session_id, task_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_session_tasks_task
    ON session_tasks(task_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_hidden_tasks (
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    hidden_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_session_hidden_tasks_task
    ON session_hidden_tasks(task_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS triage_claims (
    task_id INTEGER PRIMARY KEY,
    owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_triage_claims_expires
    ON triage_claims(expires_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_next (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL UNIQUE,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_next_lanes (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    lane_id TEXT NOT NULL,
    priority INTEGER NOT NULL,
    rationale TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT,
    UNIQUE(project_next_id, lane_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_next_tasks (
    id INTEGER PRIMARY KEY,
    project_next_lane_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    position INTEGER NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT,
    UNIQUE(project_next_lane_id, task_id),
    UNIQUE(project_next_lane_id, position),
    FOREIGN KEY (project_next_lane_id) REFERENCES project_next_lanes(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_next_pending (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(project_next_id, task_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_next_events (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    event_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    kind TEXT NOT NULL,
    payload TEXT,
    batch_id INTEGER,
    FOREIGN KEY (project_next_id) REFERENCES project_next(id),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_project_next_lanes_priority
    ON project_next_lanes(project_next_id, priority);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_project_next_events_recent
    ON project_next_events(project_next_id, event_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_project_next_pending_added
    ON project_next_pending(project_next_id, added_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_project_next_tasks_task
    ON project_next_tasks(task_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS jobs (
    name             TEXT PRIMARY KEY,
    next_due_at      TEXT NOT NULL,
    lease_owner      TEXT,
    lease_expires_at TEXT,
    last_run_at      TEXT,
    last_ok_at       TEXT,
    last_error       TEXT,
    run_count        INTEGER NOT NULL DEFAULT 0,
    fail_count       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs(next_due_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS errors (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id    INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    code          TEXT NOT NULL,
    severity      TEXT NOT NULL,
    source        TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    summary       TEXT NOT NULL,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at    TEXT,
    cleared_by    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS idx_errors_open_uniq
    ON errors(COALESCE(project_id, 0), source, code, fingerprint)
 WHERE cleared_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_errors_open
    ON errors(cleared_at, severity);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS report_labels (
    id         INTEGER PRIMARY KEY,
    gate_id    INTEGER NOT NULL REFERENCES session_gates(id) ON DELETE CASCADE,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    token      TEXT NOT NULL,
    span       TEXT,
    note       TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_report_labels_gate ON report_labels(gate_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_report_labels_token ON report_labels(token);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_minimizer_variants_type
    ON minimizer_variants(task_type, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS minimizer_champions (
    task_type   TEXT PRIMARY KEY,
    hash        TEXT NOT NULL REFERENCES minimizer_variants(hash),
    promoted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    promoted_by TEXT,
    note        TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS minimizer_evals (
    id               INTEGER PRIMARY KEY,
    task_type        TEXT NOT NULL,
    challenger_hash  TEXT NOT NULL,
    champion_hash    TEXT NOT NULL,
    corpus_ids       TEXT NOT NULL,
    wins             INTEGER NOT NULL DEFAULT 0,
    losses           INTEGER NOT NULL DEFAULT 0,
    -- Quoted because TIES is a reserved word in sqlc's SQLite grammar (it is
    -- half of SQLite's `EXCLUDE TIES` window-frame syntax). SQLite itself is
    -- happy either way and the column is identical; the quotes are what let a
    -- schema parser other than SQLite's own read this file. Verified with
    -- sqlc 1.31.1: unquoted, it is the ONLY statement in the whole schema that
    -- fails to parse. See E-2021's analysis.
    "ties"           INTEGER NOT NULL DEFAULT 0,
    vetoes           INTEGER NOT NULL DEFAULT 0,
    promoted         INTEGER NOT NULL DEFAULT 0,
    verdict          TEXT,
    detail           TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_minimizer_evals_type
    ON minimizer_evals(task_type, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS minimizer_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
-- +goose StatementEnd
