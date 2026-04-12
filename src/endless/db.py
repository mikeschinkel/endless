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


def _migrate(conn: sqlite3.Connection):
    """Run schema migrations for existing databases."""
    # Check if plan_items has title column
    cols = [
        r[1] for r in conn.execute("PRAGMA table_info(plan_items)").fetchall()
    ]
    if "title" not in cols:
        conn.execute("ALTER TABLE plan_items ADD COLUMN title TEXT")
        conn.execute(
            "UPDATE plan_items SET title = substr(task_text, 1, 80) "
            "WHERE title IS NULL"
        )
        conn.commit()

    # Create task_dependencies table if missing
    exists = conn.execute(
        "SELECT name FROM sqlite_master "
        "WHERE type='table' AND name='task_dependencies'"
    ).fetchone()
    if not exists:
        conn.executescript("""
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
                created_at TEXT NOT NULL
                    DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                UNIQUE(source_type, source_id, target_type, target_id)
            );
        """)
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
