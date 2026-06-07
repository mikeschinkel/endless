-- Endless: Project Awareness System
-- Authoritative database schema (single source of truth).
--
-- Every table the codebase relies on is defined here, in its current shape,
-- as CREATE ... IF NOT EXISTS. This file is executed on every connection
-- (monitor.DB()), so it is a no-op on a populated DB and creates everything
-- on a fresh one. Additive change (new tables / nullable columns / indexes)
-- goes here directly. Destructive, one-off change goes in a per-ticket file
-- under internal/schema/changes/, applied once at land time.
--
-- NOTE: No CHECK constraints. SQLite cannot ALTER/DROP them without
-- rebuilding the entire table, which caused catastrophic data loss.
-- All validation is done in application code (Go + Python).

PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

-- Schema-change marker. One row per applied per-ticket change file
-- (internal/schema/changes/<name>), keyed by the change's basename. Empty on
-- a fresh DB; populated by `endless db apply-change` at land time. Re-applying
-- a change is gated by the presence of its row.
CREATE TABLE IF NOT EXISTS _schema_version (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Projects
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

-- Project dependencies
CREATE TABLE IF NOT EXISTS project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'runtime',
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Notes (staleness alerts, sprawl warnings, etc.)
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

-- AI coding sessions
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    active_task_id INTEGER,
    plan_file_path TEXT,
    process TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    transcript_path TEXT,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    needs_recap INTEGER NOT NULL DEFAULT 0,
    summary_seq INTEGER NOT NULL DEFAULT 0,
    UNIQUE (session_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
);

-- E-1530 invariant: a session in state='ended' has process IS NULL. Code
-- writes also NULL process at end-of-life (Layer A); these triggers are
-- the schema-level backstop (Layer B). Required because tmux pane ids
-- (`%N`) are reused after a tmux server restart — without NULLing
-- `process` at end-of-life, lookups for new-server panes hit ghost rows
-- from the prior server. SQLite's recursive_triggers is OFF by default,
-- and the WHEN clause short-circuits anyway, so the inner UPDATE doesn't
-- recurse the AFTER UPDATE trigger.
CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_update
AFTER UPDATE OF state ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_insert
AFTER INSERT ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

-- Task items
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    parent_id INTEGER,
    title TEXT NOT NULL,
    description TEXT,
    text TEXT,
    phase TEXT NOT NULL DEFAULT 'now',
    status TEXT NOT NULL DEFAULT 'needs_plan',
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    type TEXT NOT NULL DEFAULT 'task',
    updated_at TEXT NOT NULL DEFAULT '',
    tier INTEGER,
    outcome TEXT,
    analysis TEXT,
    notes TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES tasks(id) ON DELETE SET NULL
);

CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;

-- Task dependencies (cross-project capable)
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

-- Decisions (E-1378). Lifecycle: proposed (initial) -> accepted | rejected
-- (both terminal). status validation enforced in application code.
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
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (origin_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (origin_session_id) REFERENCES sessions(id) ON DELETE SET NULL
);

CREATE TRIGGER IF NOT EXISTS decisions_updated_at AFTER UPDATE ON decisions
BEGIN
    UPDATE decisions SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;

-- Decision-sourced relations (E-1378). Source-table mapping: rows where the
-- source is a decision live here; task-sourced rows (incl. task->decision)
-- stay in task_deps until E-1389 renames. target_kind in {'task','decision'};
-- validation in application code.
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
CREATE INDEX IF NOT EXISTS idx_decision_relations_target
    ON decision_relations(target_kind, target_id);

-- Activity log (from hooks)
CREATE TABLE IF NOT EXISTS activity (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    source TEXT NOT NULL,
    working_dir TEXT,
    session_context TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- MCP channel plugin port registry
CREATE TABLE IF NOT EXISTS channels (
    process TEXT PRIMARY KEY,
    port INTEGER NOT NULL,
    pid INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Messaging conversations between paired AI sessions
CREATE TABLE IF NOT EXISTS conversations (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL UNIQUE,
    process_a TEXT NOT NULL,
    process_b TEXT,
    project_id INTEGER,
    state TEXT NOT NULL DEFAULT 'beacon',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    connected_at TEXT,
    closed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- Message queue for inter-session messaging
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sender TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    delivered_at TEXT,
    FOREIGN KEY (conversation_id) REFERENCES conversations(conversation_id) ON DELETE CASCADE
);

-- Session conversation messages (captured from JSONL transcripts via hooks)
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
CREATE INDEX IF NOT EXISTS idx_session_messages_session
    ON session_messages(session_id, created_at DESC);

-- Full-text search across session messages
CREATE VIRTUAL TABLE IF NOT EXISTS session_messages_fts USING fts5(
    content,
    content=session_messages,
    content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS session_messages_ai AFTER INSERT ON session_messages BEGIN
    INSERT INTO session_messages_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER IF NOT EXISTS session_messages_ad AFTER DELETE ON session_messages BEGIN
    INSERT INTO session_messages_fts(session_messages_fts, rowid, content) VALUES('delete', old.id, old.content);
END;

-- Files edited per task (per-task edit-set for drift detection, E-917)
CREATE TABLE IF NOT EXISTS task_files (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    first_edited_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    first_edited_session_id TEXT,
    UNIQUE(task_id, file_path),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_task_files_task ON task_files(task_id);

-- AI-agent suggestions for relaxing enforcement rules (calibration, E-918)
-- task_id IS NULL means open; populated means accepted into the referenced task.
CREATE TABLE IF NOT EXISTS suggestions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id INTEGER,
    source TEXT NOT NULL,
    trigger_ctx TEXT,
    suggestion TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    task_id INTEGER,
    notes TEXT,
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_suggestions_open ON suggestions(project_id, created_at DESC);

-- Per-task landing history (E-1337). Append-only: every successful
-- `endless worktree land` writes one row. The reaper queries
-- MAX(landed_at) per task_id to decide when a worktree dir is eligible
-- for removal. Re-landing (post-land bug fix) appends a second row;
-- the first row is preserved.
CREATE TABLE IF NOT EXISTS task_landings (
    id               INTEGER PRIMARY KEY,
    task_id          INTEGER NOT NULL,
    session_id       INTEGER,
    branch           TEXT    NOT NULL,
    merge_commit_sha TEXT    NOT NULL,
    landed_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_task_landings_task
    ON task_landings(task_id, landed_at DESC);

-- Pivot gates (E-971 Layer E). One row per pivot trigger; an "open" gate has
-- cleared_at IS NULL. cleared_by names the verb that resolved it.
CREATE TABLE IF NOT EXISTS session_gates (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    matcher_phrase TEXT NOT NULL,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_session_gates_session
    ON session_gates(session_id, triggered_at DESC);

-- Session status snapshots (E-1312 / E-1314). Latest row by created_at is the
-- current status. `tasks` holds all <task> elements; `summary` holds <layer>
-- children; active_task_id joins to tasks.id.
CREATE TABLE IF NOT EXISTS session_statuses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER,
    active_task_id INTEGER,
    created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    headline TEXT,
    summary TEXT,
    tasks TEXT,
    decisions TEXT,
    commits TEXT,
    memory TEXT,
    notes TEXT,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS session_statuses_session_recent_idx
    ON session_statuses (session_id, created_at DESC);

-- Which sessions touched which tasks (E-1322). Query-speed projection of the
-- events ledger. No FKs by design: rows must outlive their referenced
-- session/task so the "session N touched task M" record survives deletion.
CREATE TABLE IF NOT EXISTS session_tasks (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(session_id, task_id)
);
CREATE INDEX IF NOT EXISTS idx_session_tasks_task
    ON session_tasks(task_id);

-- Curated, persistent per-project "next" list (E-1421). Five tables: header,
-- lanes, items, auto-added pending items awaiting curation, and a revision
-- audit trail.
CREATE TABLE IF NOT EXISTS project_next (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL UNIQUE,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_lanes (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    lane_id TEXT NOT NULL,
    priority INTEGER NOT NULL,
    rationale TEXT NOT NULL,
    UNIQUE(project_next_id, lane_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_items (
    id INTEGER PRIMARY KEY,
    project_next_lane_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    position INTEGER NOT NULL,
    UNIQUE(project_next_lane_id, task_id),
    UNIQUE(project_next_lane_id, position),
    FOREIGN KEY (project_next_lane_id) REFERENCES project_next_lanes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_pending (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(project_next_id, task_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_revisions (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    revised_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    change_kind TEXT NOT NULL,
    json_snapshot TEXT,
    FOREIGN KEY (project_next_id) REFERENCES project_next(id),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_project_next_lanes_priority
    ON project_next_lanes(project_next_id, priority);
CREATE INDEX IF NOT EXISTS idx_project_next_revisions_recent
    ON project_next_revisions(project_next_id, revised_at DESC);
CREATE INDEX IF NOT EXISTS idx_project_next_pending_added
    ON project_next_pending(project_next_id, added_at);
CREATE INDEX IF NOT EXISTS idx_project_next_items_task
    ON project_next_items(task_id);
