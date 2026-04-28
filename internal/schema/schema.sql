-- Endless: Project Awareness System
-- SQLite Schema v3
-- NOTE: No CHECK constraints. SQLite cannot ALTER/DROP them without
-- rebuilding the entire table, which caused catastrophic data loss.
-- All validation is done in application code (Go + Python).

PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

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

-- Task items
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    parent_id INTEGER,
    title TEXT NOT NULL,
    description TEXT,
    text TEXT,
    prompt TEXT,
    phase TEXT NOT NULL DEFAULT 'now',
    status TEXT NOT NULL DEFAULT 'needs_plan',
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    type TEXT NOT NULL DEFAULT 'task',
    updated_at TEXT NOT NULL DEFAULT '',
    tier INTEGER,
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
    UNIQUE(source_type, source_id, target_type, target_id)
);

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
