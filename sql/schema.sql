-- Endless: Project Awareness System
-- SQLite Schema v1

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Projects
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    label TEXT,
    path TEXT NOT NULL UNIQUE,
    group_name TEXT,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'archived', 'idea', 'unregistered', 'anonymous')),
    language TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Project dependencies
CREATE TABLE IF NOT EXISTS project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'runtime'
        CHECK (dep_type IN ('runtime', 'dev', 'tooling')),
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Documents within projects
CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    doc_type TEXT NOT NULL DEFAULT 'other',
    content_hash TEXT,
    size_bytes INTEGER,
    last_modified TEXT,
    last_scanned TEXT,
    is_archived INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT,
    UNIQUE (project_id, relative_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Document dependency rules
CREATE TABLE IF NOT EXISTS doc_dependencies (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    dependent_doc TEXT NOT NULL,
    dependent_region TEXT,
    depends_on TEXT NOT NULL,
    dep_kind TEXT NOT NULL
        CHECK (dep_kind IN ('content', 'code', 'structural')),
    learned INTEGER NOT NULL DEFAULT 0,
    confidence REAL NOT NULL DEFAULT 1.0,
    confirmed INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Region definitions within documents
CREATE TABLE IF NOT EXISTS doc_regions (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    document_path TEXT NOT NULL,
    region_id TEXT NOT NULL,
    region_type TEXT NOT NULL
        CHECK (region_type IN ('heading', 'fenced_block', 'frontmatter_field', 'custom')),
    start_marker TEXT,
    content_hash TEXT,
    last_modified TEXT,
    learned INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Notes (staleness alerts, sprawl warnings, etc.)
CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    note_type TEXT NOT NULL
        CHECK (note_type IN ('staleness', 'update_needed', 'sprawl', 'privacy', 'general')),
    message TEXT NOT NULL,
    source TEXT,
    target_doc TEXT,
    resolved INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    resolved_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- tmux sessions
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY,
    session_name TEXT NOT NULL,
    window_name TEXT,
    working_dir TEXT,
    project_id INTEGER,
    first_seen TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    is_active INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- AI chat conversations (from browser extension)
CREATE TABLE IF NOT EXISTS ai_chats (
    id INTEGER PRIMARY KEY,
    platform TEXT NOT NULL
        CHECK (platform IN ('claude', 'chatgpt')),
    chat_id TEXT,
    title TEXT,
    project_id INTEGER,
    classification_confidence REAL,
    started_at TEXT,
    last_message_at TEXT,
    message_count INTEGER,
    summary TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- AI coding sessions (Claude Code, Codex)
CREATE TABLE IF NOT EXISTS ai_sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude'
        CHECK (platform IN ('claude', 'codex')),
    state TEXT NOT NULL DEFAULT 'working'
        CHECK (state IN ('working', 'idle', 'needs_input', 'ended')),
    working_dir TEXT,
    transcript_path TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    ended_at TEXT,
    UNIQUE (session_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- Plan items (imported from plan files, managed by Endless)
CREATE TABLE IF NOT EXISTS plan_items (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    phase TEXT NOT NULL DEFAULT 'now',
    title TEXT,
    task_text TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'completed', 'blocked')),
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    plan_id INTEGER NOT NULL DEFAULT 0,
    parent_item_id INTEGER,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_item_id) REFERENCES plan_items(id) ON DELETE SET NULL
);

-- Task dependencies (cross-project capable)
CREATE TABLE IF NOT EXISTS task_dependencies (
    id INTEGER PRIMARY KEY,
    source_type TEXT NOT NULL
        CHECK (source_type IN ('task', 'plan', 'project')),
    source_id INTEGER NOT NULL,
    target_type TEXT NOT NULL
        CHECK (target_type IN ('task', 'plan', 'project')),
    target_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'blocks'
        CHECK (dep_type IN ('blocks', 'needs')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_type, source_id, target_type, target_id)
);

-- Scan history
CREATE TABLE IF NOT EXISTS scan_log (
    id INTEGER PRIMARY KEY,
    scan_type TEXT NOT NULL
        CHECK (scan_type IN ('full', 'incremental', 'documents', 'sessions', 'discover')),
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    projects_scanned INTEGER,
    changes_detected INTEGER
);

-- Private file tracking
CREATE TABLE IF NOT EXISTS private_files (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    content_hash TEXT,
    last_synced TEXT,
    companion_repo TEXT,
    UNIQUE (project_id, relative_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Activity log (from hooks)
CREATE TABLE IF NOT EXISTS activity (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    source TEXT NOT NULL
        CHECK (source IN ('prompt', 'claude', 'codex')),
    working_dir TEXT,
    session_context TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- File change log (from hooks)
CREATE TABLE IF NOT EXISTS file_changes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    change_type TEXT NOT NULL
        CHECK (change_type IN ('new', 'modified', 'deleted', 'renamed')),
    old_path TEXT,
    detected_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    source TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Learned privacy criteria
CREATE TABLE IF NOT EXISTS privacy_rules (
    id INTEGER PRIMARY KEY,
    pattern TEXT NOT NULL,
    rule_type TEXT NOT NULL
        CHECK (rule_type IN ('filename_pattern', 'content_keyword', 'directory')),
    confidence REAL NOT NULL DEFAULT 0.5,
    confirmed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
