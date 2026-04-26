"""SQLite database helpers."""

import sqlite3
from pathlib import Path

from endless.config import DB_PATH, ensure_config_dir

_conn: sqlite3.Connection | None = None

# Find schema.sql relative to this package
_SCHEMA_PATH = Path(__file__).resolve().parent.parent.parent / "sql" / "schema.sql"


def get_db() -> sqlite3.Connection:
    global _conn
    if _conn is not None:
        return _conn
    ensure_config_dir()
    is_new = not DB_PATH.exists()
    _conn = sqlite3.connect(str(DB_PATH))
    _conn.row_factory = sqlite3.Row
    _conn.execute("PRAGMA journal_mode=WAL")
    _conn.execute("PRAGMA busy_timeout=5000")
    _conn.execute("PRAGMA foreign_keys=ON")
    if is_new:
        _init_schema(_conn)
    else:
        _migrate(_conn)
    return _conn


def _init_schema(conn: sqlite3.Connection):
    if not _SCHEMA_PATH.exists():
        raise FileNotFoundError(f"Schema not found: {_SCHEMA_PATH}")
    schema = _SCHEMA_PATH.read_text()
    conn.executescript(schema)


def _backup_db():
    """Backup DB using SQLite backup API if last backup is > 60 seconds old. Keeps last 60."""
    import time as _time

    if not DB_PATH.exists():
        return

    backup_dir = DB_PATH.parent / "backups"
    backup_dir.mkdir(exist_ok=True)

    # Check if recent backup exists
    backups = sorted(backup_dir.glob("endless-*.db"))
    if backups:
        newest = backups[-1]
        age = _time.time() - newest.stat().st_mtime
        if age < 60:
            return

    # Use SQLite backup API for a consistent copy
    ts = _time.strftime("%Y%m%d-%H%M%S")
    dst = backup_dir / f"endless-{ts}.db"
    src_conn = sqlite3.connect(str(DB_PATH))
    dst_conn = sqlite3.connect(str(dst))
    src_conn.backup(dst_conn)
    dst_conn.close()
    src_conn.close()

    # Rotate: keep last 60
    backups = sorted(backup_dir.glob("endless-*.db"))
    if len(backups) > 60:
        for old in backups[:-60]:
            old.unlink()


def _migrate(conn: sqlite3.Connection):
    """Run schema migrations for existing databases."""
    _backup_db()  # backup before any migration
    # Rename plans table to tasks if needed
    tables = [
        r[0]
        for r in conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('plans','tasks')"
        ).fetchall()
    ]
    if "plans" in tables and "tasks" not in tables:
        conn.execute("ALTER TABLE plans RENAME TO tasks")
        conn.commit()

    # Add type column to tasks if missing
    task_cols = [
        r[1] for r in conn.execute("PRAGMA table_info(tasks)").fetchall()
    ]
    if "type" not in task_cols:
        conn.execute(
            "ALTER TABLE tasks ADD COLUMN type TEXT NOT NULL DEFAULT 'task'"
        )
        conn.commit()

    # Rename plan_id to task_id if needed
    if "plan_id" in task_cols and "task_id" not in task_cols:
        conn.execute("ALTER TABLE tasks RENAME COLUMN plan_id TO task_id")
        conn.commit()

    # Add updated_at column to tasks if missing
    task_cols2 = [
        r[1] for r in conn.execute("PRAGMA table_info(tasks)").fetchall()
    ]
    if "updated_at" not in task_cols2:
        conn.execute(
            "ALTER TABLE tasks ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''"
        )
        conn.execute("UPDATE tasks SET updated_at = created_at WHERE updated_at = ''")
        conn.executescript("""
            CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
            BEGIN
                UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
                WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
            END;
        """)
        conn.commit()

    # Check if tasks has title column
    cols = [
        r[1] for r in conn.execute("PRAGMA table_info(tasks)").fetchall()
    ]
    if "title" not in cols:
        conn.execute("ALTER TABLE tasks ADD COLUMN title TEXT")
        conn.execute(
            "UPDATE tasks SET title = substr(description, 1, 80) "
            "WHERE title IS NULL"
        )
        conn.commit()

    # Create task_deps table if missing (handles both old and new name)
    exists = conn.execute(
        "SELECT name FROM sqlite_master "
        "WHERE type='table' AND name = 'task_deps'"
    ).fetchone()
    if not exists:
        conn.executescript("""
            CREATE TABLE IF NOT EXISTS task_deps (
                id INTEGER PRIMARY KEY,
                source_type TEXT NOT NULL
                    CHECK (source_type IN ('task', 'project')),
                source_id INTEGER NOT NULL,
                target_type TEXT NOT NULL
                    CHECK (target_type IN ('task', 'project')),
                target_id INTEGER NOT NULL,
                dep_type TEXT NOT NULL DEFAULT 'blocks'
                    CHECK (dep_type IN ('blocks', 'needs')),
                created_at TEXT NOT NULL
                    DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                UNIQUE(source_type, source_id, target_type, target_id)
            );
        """)
        conn.commit()

    # === Schema v2 migrations ===
    _migrate_v2(conn)

    # === Schema v3: Session conversation history (E-857) ===
    _migrate_v3(conn)


def _has_table(conn: sqlite3.Connection, table: str) -> bool:
    row = conn.execute(
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?",
        (table,),
    ).fetchone()
    return row[0] > 0


def _has_column(conn: sqlite3.Connection, table: str, column: str) -> bool:
    cols = [r[1] for r in conn.execute(f"PRAGMA table_info({table})").fetchall()]
    return column in cols


def _migrate_v2(conn: sqlite3.Connection):
    """Schema v2: drop dead tables, rename tables/columns, drop unused columns."""
    # Step 1: Drop dead tables (E-741)
    for table in [
        "doc_dependencies", "doc_regions", "ai_chats",
        "private_files", "privacy_rules", "claude_sessions",
        "file_changes", "scan_log", "documents",
    ]:
        conn.execute(f"DROP TABLE IF EXISTS {table}")
    # Drop old sessions table (ZSH prompt hook) if ai_sessions still exists
    if _has_table(conn, "sessions") and _has_table(conn, "ai_sessions"):
        conn.execute("DROP TABLE sessions")
    conn.commit()

    # Step 2: Rename tables (E-742)
    if _has_table(conn, "msg_queue") and not _has_table(conn, "messages"):
        conn.execute("ALTER TABLE msg_queue RENAME TO messages")
    if _has_table(conn, "msg_channels") and not _has_table(conn, "conversations"):
        conn.execute("ALTER TABLE msg_channels RENAME TO conversations")
    if _has_table(conn, "ai_sessions") and not _has_table(conn, "sessions"):
        conn.execute("ALTER TABLE ai_sessions RENAME TO sessions")
    conn.commit()

    # Step 3: Rename columns (E-743)
    if _has_table(conn, "sessions"):
        if _has_column(conn, "sessions", "active_goal_id") and not _has_column(conn, "sessions", "active_task_id"):
            conn.execute("ALTER TABLE sessions RENAME COLUMN active_goal_id TO active_task_id")
        if _has_column(conn, "sessions", "tmux_pane") and not _has_column(conn, "sessions", "process"):
            conn.execute("ALTER TABLE sessions RENAME COLUMN tmux_pane TO process")
    if _has_table(conn, "conversations"):
        if _has_column(conn, "conversations", "channel_id"):
            conn.execute("ALTER TABLE conversations RENAME COLUMN channel_id TO conversation_id")
        if _has_column(conn, "conversations", "pane_a"):
            conn.execute("ALTER TABLE conversations RENAME COLUMN pane_a TO process_a")
        if _has_column(conn, "conversations", "pane_b"):
            conn.execute("ALTER TABLE conversations RENAME COLUMN pane_b TO process_b")
    if _has_table(conn, "messages"):
        if _has_column(conn, "messages", "channel_id"):
            conn.execute("ALTER TABLE messages RENAME COLUMN channel_id TO conversation_id")
    conn.commit()

    # Steps 4-12: Table rebuild migrations — MOVED TO MANUAL
    # These previously ran automatically but caused data loss when rebuild
    # migrations dropped columns or failed to copy new columns.
    # Now only safe data UPDATEs run automatically.
    # Run 'endless db migrate' for table rebuilds (with backup).

    # Safe data updates from former rebuild migrations:
    if _has_table(conn, "task_deps"):
        conn.execute("UPDATE task_deps SET source_type='task' WHERE source_type='plan'")
        conn.execute("UPDATE task_deps SET target_type='task' WHERE target_type='plan'")
        conn.commit()

    # Step 8: Add 'tier' column to tasks (E-786) — safe ADD COLUMN
    if _has_table(conn, "tasks"):
        cols = [
            r[1] for r in conn.execute("PRAGMA table_info(tasks)").fetchall()
        ]
        if "tier" not in cols:
            conn.execute("ALTER TABLE tasks ADD COLUMN tier INTEGER")
            conn.commit()

    # Safe data updates: rename completed → confirmed, fix tier 1 status
    if _has_table(conn, "tasks"):
        conn.execute("UPDATE tasks SET status = 'confirmed' WHERE status = 'completed'")
        conn.execute(
            "UPDATE tasks SET completed_at = NULL "
            "WHERE completed_at IS NOT NULL AND status != 'confirmed'"
        )
        conn.execute(
            "UPDATE tasks SET status = 'ready' "
            "WHERE tier = 1 AND status = 'needs_plan'"
        )
        conn.commit()

    # Step 13: Clear tier to 0 (n/a) on terminal and verify tasks (E-856)
    if _has_table(conn, "tasks"):
        conn.execute(
            "UPDATE tasks SET tier = 0 "
            "WHERE tier IS NOT NULL AND tier != 0 "
            "AND status IN ('verify', 'confirmed', 'assumed', 'declined', 'obsolete')"
        )
        conn.commit()

    # Safety net: ensure sessions table exists
    # Handles edge cases where partial migrations left the table missing
    if not _has_table(conn, "sessions"):
        conn.executescript("""
            CREATE TABLE IF NOT EXISTS sessions (
                id INTEGER PRIMARY KEY,
                session_id TEXT NOT NULL,
                project_id INTEGER,
                platform TEXT NOT NULL DEFAULT 'claude'
                    CHECK (platform IN ('claude', 'codex')),
                state TEXT NOT NULL DEFAULT 'working'
                    CHECK (state IN ('working', 'idle', 'needs_input', 'ended')),
                active_task_id INTEGER,
                plan_file_path TEXT,
                process TEXT,
                started_at TEXT NOT NULL
                    DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                last_activity TEXT,
                UNIQUE (session_id),
                FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
                FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
            );
        """)
        conn.commit()


def _migrate_v3(conn: sqlite3.Connection):
    """Schema v3: session conversation messages + FTS5."""
    # session_messages table
    if not _has_table(conn, "session_messages"):
        conn.executescript("""
            CREATE TABLE IF NOT EXISTS session_messages (
                id INTEGER PRIMARY KEY,
                session_id TEXT NOT NULL,
                role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool_use')),
                content TEXT NOT NULL,
                tool_name TEXT,
                message_uuid TEXT UNIQUE,
                created_at TEXT NOT NULL,
                FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
            );
            CREATE INDEX IF NOT EXISTS idx_session_messages_session
                ON session_messages(session_id, created_at DESC);
        """)
        conn.commit()

    # FTS5 for cross-session search
    if not _has_table(conn, "session_messages_fts"):
        conn.executescript("""
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
        """)
        conn.commit()

    # Add new columns to sessions
    if _has_table(conn, "sessions"):
        cols = [r[1] for r in conn.execute("PRAGMA table_info(sessions)").fetchall()]
        if "transcript_offset" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN transcript_offset INTEGER NOT NULL DEFAULT 0")
            conn.commit()
        if "transcript_path" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN transcript_path TEXT")
            conn.commit()
        if "summary" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN summary TEXT")
            conn.commit()
        if "hidden" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0")
            conn.commit()
        if "needs_recap" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN needs_recap INTEGER NOT NULL DEFAULT 0")
            conn.commit()
        if "summary_seq" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN summary_seq INTEGER NOT NULL DEFAULT 0")
            conn.commit()


def execute(sql: str, params: tuple = ()) -> sqlite3.Cursor:
    db = get_db()
    cursor = db.execute(sql, params)
    db.commit()
    return cursor


def query(sql: str, params: tuple = ()) -> list[sqlite3.Row]:
    return get_db().execute(sql, params).fetchall()


def scalar(sql: str, params: tuple = ()):
    row = get_db().execute(sql, params).fetchone()
    if row is None:
        return None
    return row[0]


def exists(sql: str, params: tuple = ()) -> bool:
    return scalar(sql, params) is not None
