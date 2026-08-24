"""SQLite database helpers."""

import os
import re
import sqlite3
from pathlib import Path
from typing import NamedTuple

import click

from endless import config
from endless.config import ensure_config_dir

_conn: sqlite3.Connection | None = None

# Find schema.sql relative to this package (temporary until E-894 moves all SQL to Go)
_SCHEMA_PATH = Path(__file__).resolve().parent.parent.parent / "internal" / "schema" / "schema.sql"

# The per-ticket schema changes that carry an existing DB from one shape to the
# next, beside the schema they amend. Present when endless is installed from a
# source checkout (`just install` installs the Python CLI editable), absent when
# it is not — every reader below tolerates the absence (E-2036).
_CHANGES_DIR = _SCHEMA_PATH.parent / "changes"


def _should_auto_migrate() -> bool:
    val = os.environ.get("ENDLESS_AUTO_MIGRATE", "1").strip().lower()
    return val in ("1", "true", "yes", "on")


def get_db() -> sqlite3.Connection:
    global _conn
    # E-1429: refuse a direct DB read inside a self-dev worktree unless an
    # explicit --db was resolved. Choke point for the Python-side gate.
    config.require_db_context()
    if _conn is not None:
        return _conn
    ensure_config_dir()
    is_new = not config.DB_PATH.exists()
    _conn = sqlite3.connect(str(config.DB_PATH))
    _conn.row_factory = sqlite3.Row
    # Decode TEXT leniently (E-1914). sqlite3's default text_factory raises
    # OperationalError on a column holding invalid UTF-8, and it raises for the
    # whole QUERY — one damaged byte anywhere in the result set takes down the
    # command, naming a column the user did not ask about.
    #
    # SQLite does not validate what it stores, so a writer that truncated a
    # string by BYTE length could leave a half-encoded codepoint behind. The
    # known instance is sessions.summary, written by the recap generator removed
    # in E-1906 — inert historical damage no current code can add to, but present
    # in every long-lived DB and enough to crash `session list` and `session show`
    # on the one row that has it.
    #
    # Applied at the connection, not per query, because the failure mode belongs
    # to reading TEXT at all: fixing it per-column is whack-a-mole against data
    # nothing can repair from here. U+FFFD marks the damage visibly instead of
    # hiding it.
    _conn.text_factory = lambda b: b.decode("utf-8", "replace")
    _conn.execute("PRAGMA journal_mode=WAL")
    _conn.execute("PRAGMA busy_timeout=5000")
    _conn.execute("PRAGMA foreign_keys=ON")
    if is_new:
        _init_schema(_conn)
    elif not _has_table(_conn, "projects"):
        # File exists but lacks the foundational schema. Don't try to migrate
        # (it would crash with a raw OperationalError). Surface a clear error
        # naming the resolved path and resolution mechanism.
        _conn.close()
        _conn = None
        raise _missing_schema_hint()
    elif _should_auto_migrate():
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

    if not config.DB_PATH.exists():
        return

    backup_dir = config.DB_PATH.parent / "backups"
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
    src_conn = sqlite3.connect(str(config.DB_PATH))
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
    """Run schema migrations for existing databases.

    Short-circuits when PRAGMA user_version is already >= 6 (the highest
    version this Python migrator knows about). Once Go's framework
    (internal/monitor/migrate.go, E-863) owns the schema, this function
    becomes a no-op. Slated for removal in E-894 Phase 5.
    """
    if conn.execute("PRAGMA user_version").fetchone()[0] >= 6:
        return
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
                source_type TEXT NOT NULL,
                source_id INTEGER NOT NULL,
                target_type TEXT NOT NULL,
                target_id INTEGER NOT NULL,
                dep_type TEXT NOT NULL DEFAULT 'blocks',
                created_at TEXT NOT NULL
                    DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                UNIQUE(source_type, source_id, target_type, target_id, dep_type)
            );
        """)
        conn.commit()

    # === Schema v2 migrations ===
    _migrate_v2(conn)

    # === Schema v3: Session conversation history (E-857) ===
    _migrate_v3(conn)

    # === Schema v5: task_deps active-voice vocabulary (E-957) ===
    _migrate_v5(conn)

    # === Schema v6: outcome column on tasks (E-787) ===
    _migrate_v6(conn)


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

    # Step 2: Rename tables (E-742). The msg_queue -> messages and
    # msg_channels -> conversations renames went with the channel surface
    # (E-2029); e-2029-drop-channel-tables.sql drops both names outright.
    if _has_table(conn, "ai_sessions") and not _has_table(conn, "sessions"):
        conn.execute("ALTER TABLE ai_sessions RENAME TO sessions")
    conn.commit()

    # Step 3: Rename columns (E-743). Left at the name E-743 produced: this is
    # one link of a chain, not a declaration of the current shape. E-1969 renamed
    # active_task_id -> task_id, and its change file
    # (internal/schema/changes/e-1969-rename-sessions-task-id.go) picks up from
    # here, so rewriting this step's target would break the chain rather than
    # shorten it.
    if _has_table(conn, "sessions"):
        if _has_column(conn, "sessions", "active_goal_id") and not _has_column(conn, "sessions", "active_task_id"):
            conn.execute("ALTER TABLE sessions RENAME COLUMN active_goal_id TO active_task_id")
        if _has_column(conn, "sessions", "tmux_pane") and not _has_column(conn, "sessions", "process"):
            conn.execute("ALTER TABLE sessions RENAME COLUMN tmux_pane TO process")
    conn.commit()

    # Steps 4-12: Table rebuild migrations — MOVED OUT
    # These previously ran automatically but caused data loss when rebuild
    # migrations dropped columns or failed to copy new columns.
    # Now only safe data UPDATEs run automatically. Destructive, one-off
    # changes live in internal/schema/changes/ and are applied at land time
    # via 'endless db apply-change'.

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

    # Safe data updates: fix completed_at on non-confirmed (legacy completed->confirmed
    # rename removed in E-1240; `completed` is once again a real terminal status with
    # findings-as-deliverable semantics, distinct from `confirmed`).
    if _has_table(conn, "tasks"):
        conn.execute(
            "UPDATE tasks SET completed_at = NULL "
            "WHERE completed_at IS NOT NULL AND status NOT IN ('confirmed', 'completed')"
        )
        conn.execute(
            "UPDATE tasks SET status = 'ready' "
            "WHERE tier = 1 AND status = 'unplanned'"
        )
        conn.commit()

    # Step 13: Clear tier to 0 (n/a) on terminal and unverified tasks (E-856, E-1240)
    if _has_table(conn, "tasks"):
        conn.execute(
            "UPDATE tasks SET tier = 0 "
            "WHERE tier IS NOT NULL AND tier != 0 "
            "AND status IN ('unverified', 'confirmed', 'assumed', 'completed', 'declined', 'obsolete')"
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
                task_id INTEGER,
                plan_file_path TEXT,
                process TEXT,
                started_at TEXT NOT NULL
                    DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                last_activity TEXT,
                UNIQUE (session_id),
                FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
                FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
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
        # transcript_path was here until E-1905 dropped the column. Do NOT
        # re-add it: this migration runs on every Python-side connect, so an
        # ADD COLUMN here would silently resurrect the column right after the
        # land-time change file drops it.
        if "summary" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN summary TEXT")
            conn.commit()
        if "hidden" not in cols:
            conn.execute("ALTER TABLE sessions ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0")
            conn.commit()
        # needs_recap / summary_seq were added here for the session-recap
        # machinery, removed in E-1906. Their ALTERs are gone rather than
        # merely unused: this migrator can still run on a pre-v6 DB, and
        # re-adding the columns there would silently undo the drop that
        # e-1906-drop-sessions-recap-columns.sql applies at land time.


def _migrate_v5(conn: sqlite3.Connection):
    """Schema v5: task_deps active-voice vocabulary (E-957).

    Three changes:
    1. Drop legacy CHECK constraints on task_deps (source_type, target_type, dep_type)
       so new dep_types like 'implements', 'informs', 'relates_to' can be inserted.
    2. Expand UNIQUE constraint to include dep_type so multiple typed relations
       can coexist between the same ordered pair (e.g. A blocks B AND A relates_to B).
    3. Migrate existing rows to active-voice storage:
       - 'needs'/'blocks' rows → 'blocks' with source/target swapped (source becomes blocker)
       - 'replaces' rows → swap source/target (label was already correct, layout was passive)
    Both UPDATEs evaluate RHS against the original row, so source/target swap atomically.
    """
    # Idempotency gate: the swap UPDATEs below are NOT idempotent (running them
    # twice flips rows back). Use PRAGMA user_version as a one-shot guard so V5
    # only runs once. See E-1118 / E-863 for the structural fix (real schema
    # version system).
    if conn.execute("PRAGMA user_version").fetchone()[0] >= 5:
        return
    if not _has_table(conn, "task_deps"):
        conn.execute("PRAGMA user_version = 5")
        conn.commit()
        return

    sql_row = conn.execute(
        "SELECT sql FROM sqlite_master WHERE type='table' AND name='task_deps'"
    ).fetchone()
    table_sql = sql_row[0] if sql_row is not None else ""
    has_check = "CHECK" in table_sql
    # Old UNIQUE constraint omits dep_type; new one includes it.
    needs_unique_rebuild = (
        "UNIQUE(source_type, source_id, target_type, target_id, dep_type)" not in table_sql
    )

    if has_check or needs_unique_rebuild:
        # SQLite has no DROP CHECK; rebuild the table without the constraint.
        # executescript implicitly commits before running, so we use individual
        # execute() calls inside an explicit transaction.
        conn.execute("PRAGMA foreign_keys=OFF")
        try:
            conn.execute("""
                CREATE TABLE task_deps_new (
                    id INTEGER PRIMARY KEY,
                    source_type TEXT NOT NULL,
                    source_id INTEGER NOT NULL,
                    target_type TEXT NOT NULL,
                    target_id INTEGER NOT NULL,
                    dep_type TEXT NOT NULL DEFAULT 'blocks',
                    created_at TEXT NOT NULL
                        DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
                    UNIQUE(source_type, source_id, target_type, target_id, dep_type)
                )
            """)
            conn.execute(
                "INSERT INTO task_deps_new "
                "(id, source_type, source_id, target_type, target_id, dep_type, created_at) "
                "SELECT id, source_type, source_id, target_type, target_id, dep_type, created_at "
                "FROM task_deps"
            )
            conn.execute("DROP TABLE task_deps")
            conn.execute("ALTER TABLE task_deps_new RENAME TO task_deps")
            conn.commit()
        except Exception:
            conn.rollback()
            conn.execute("PRAGMA foreign_keys=ON")
            raise
        conn.execute("PRAGMA foreign_keys=ON")

    # Active-voice migration: needs/blocks rows store source=blocker, target=blocked.
    # Today's data has source=blocked, target=blocker, dep_type='needs'. Swap and rename.
    try:
        conn.execute("""
            UPDATE task_deps
            SET    source_id = target_id,
                   target_id = source_id,
                   dep_type  = 'blocks'
            WHERE  dep_type IN ('needs', 'blocks')
        """)
        # replaces rows: label was active ('replaces') but layout was passive
        # (source=replaced_task, target=replacement). Swap source/target so the row reads
        # "source replaces target" — matching the label and the active-voice convention.
        conn.execute("""
            UPDATE task_deps
            SET    source_id = target_id,
                   target_id = source_id
            WHERE  dep_type = 'replaces'
        """)
        # E-1003: informs/informed_by dropped from canonical vocabulary as too vague.
        # Existing rows fold into relates_to (the soft catch-all). No source/target swap
        # needed; both informs and relates_to store source=actor with same direction.
        conn.execute(
            "UPDATE task_deps SET dep_type='relates_to' WHERE dep_type='informs'"
        )
        conn.execute("PRAGMA user_version = 5")
        conn.commit()
    except sqlite3.IntegrityError as e:
        conn.rollback()
        raise RuntimeError(
            "task_deps active-voice migration aborted: UNIQUE collision after swap. "
            "Two tasks may have mirrored relations (A blocks B AND B blocks A as separate rows). "
            f"Backup at ~/.endless/backups/. Original error: {e}"
        )


def _migrate_v6(conn: sqlite3.Connection):
    """Schema v6: add outcome column to tasks (E-787)."""
    if _has_table(conn, "tasks") and not _has_column(conn, "tasks", "outcome"):
        conn.execute("ALTER TABLE tasks ADD COLUMN outcome TEXT")
        conn.commit()


class _MissingObject(NamedTuple):
    """One table or column the database does not have, as sqlite3 named it."""

    kind: str           # "table" or "column"
    name: str           # exactly as sqlite3 named it, qualifier and all
    table: str | None   # the owning table, on the one error form that carries it

    @property
    def ident(self) -> str:
        """The bare identifier: 'd.superseded_by' names the column 'superseded_by'."""
        return self.name.rsplit(".", 1)[-1]

    @property
    def label(self) -> str:
        """How the message names it — qualified by its table when that is known.

        The alias sqlite3 echoes back ('d.superseded_by') is dropped: 'd' is a
        query-local name for a table the reader would have to reconstruct, and
        the failing statement is printed underneath anyway.
        """
        return f"{self.table}.{self.ident}" if self.table else self.ident


_NO_SUCH_TABLE_RE = re.compile(r"^no such table:\s*(\S+)", re.IGNORECASE)
_NO_SUCH_COLUMN_RE = re.compile(r"^no such column:\s*(\S+)", re.IGNORECASE)
_NO_COLUMN_NAMED_RE = re.compile(
    r"^table\s+(\S+)\s+has no column named\s+(\S+)", re.IGNORECASE
)


def _classify_schema_error(err: sqlite3.OperationalError) -> _MissingObject | None:
    """Name what the database is missing, or None if that isn't what failed.

    sqlite3 phrases it three ways: "no such table: X" and "no such column: X"
    from a SELECT/UPDATE/ORDER BY, and "table T has no column named C" from an
    INSERT. Everything else — syntax errors, locking, per-row failures — is a
    real error to keep raising.
    """
    msg = str(err).strip()
    m = _NO_SUCH_TABLE_RE.match(msg)
    if m:
        return _MissingObject("table", m.group(1), None)
    m = _NO_SUCH_COLUMN_RE.match(msg)
    if m:
        return _MissingObject("column", m.group(1), None)
    m = _NO_COLUMN_NAMED_RE.match(msg)
    if m:
        return _MissingObject("column", m.group(2), m.group(1))
    return None


def _change_files() -> list[Path]:
    """Every per-ticket schema-change file this install ships.

    runner/ is a directory (library code, not a change), so is_file() excludes
    it. Empty when the directory is absent, which is the case for an install
    that is a copy of src/endless/ rather than a source checkout — the hint
    below degrades to naming the missing column and nothing more.
    """
    if not _CHANGES_DIR.is_dir():
        return []
    return sorted(
        p for p in _CHANGES_DIR.iterdir()
        if p.is_file() and p.suffix in (".sql", ".go")
    )


def _unapplied_changes(conn: sqlite3.Connection) -> list[Path]:
    """The change files with no _schema_version marker in THIS database.

    The marker key is the file's basename without extension — the same key
    `endless db apply-change` computes (internal/schema/changes/runner). A DB
    predating the marker table has applied nothing we can prove, so every file
    counts as outstanding.
    """
    files = _change_files()
    if not files:
        return []
    applied: set[str] = set()
    if _has_table(conn, "_schema_version"):
        applied = {r[0] for r in conn.execute("SELECT name FROM _schema_version")}
    return [p for p in files if p.stem not in applied]


def _changes_naming(
    missing: _MissingObject, candidates: list[Path]
) -> tuple[list[Path], bool]:
    """The candidate changes that name the missing object, and how they name it.

    Two tiers, because they are worth different confidence. A change whose text
    carries the DDL that creates the object *adds* it — that is the file to
    apply, and the flag says so. A change that merely mentions the identifier
    (a .go change explaining an ordering constraint in a comment, say) is a
    lead, not an answer, and the message words it as one.

    The DDL match is a text match, not a parse: a `.go` change builds the same
    statement as a string, so one pattern covers both file kinds.
    """
    ident = re.escape(missing.ident)
    if missing.kind == "column":
        ddl = re.compile(rf"ADD\s+(?:COLUMN\s+)?[\"'`\[]?{ident}\b", re.IGNORECASE)
    else:
        ddl = re.compile(
            rf"CREATE\s+(?:TABLE|VIEW)\s+(?:IF\s+NOT\s+EXISTS\s+)?[\"'`\[]?{ident}\b",
            re.IGNORECASE,
        )
    mention = re.compile(rf"\b{ident}\b")
    adders, mentions = [], []
    for path in candidates:
        try:
            text = path.read_text(errors="replace")
        except OSError:
            continue
        if ddl.search(text):
            adders.append(path)
        elif mention.search(text):
            mentions.append(path)
    if adders:
        return adders, True
    return mentions, False


def _sql_excerpt(sql: str, limit: int = 160) -> str:
    """The failing statement on one line, short enough to read."""
    one_line = " ".join(sql.split())
    if len(one_line) <= limit:
        return one_line
    return one_line[:limit - 1] + "…"


def _schema_error_hint(
    err: sqlite3.OperationalError, sql: str
) -> click.ClickException | None:
    """Diagnose a failed statement, or None to let the error through unchanged.

    E-2036: one missing column used to be reported as an uninitialized
    database — the message named XDG_CONFIG_HOME and the db file's byte count
    while `decision list`, which did not select that column, worked fine in the
    same second. A database that HAS the endless schema and lacks one object is
    a different problem with a different fix, so it gets a different message.
    """
    missing = _classify_schema_error(err)
    if missing is None:
        return None
    conn = _conn
    if conn is None or not _has_table(conn, "projects"):
        return _missing_schema_hint()
    return _incomplete_schema_hint(conn, missing, sql)


def _incomplete_schema_hint(
    conn: sqlite3.Connection, missing: _MissingObject, sql: str
) -> click.ClickException:
    """Build the message for a schema'd database missing one table or column.

    Two outcomes, because they need opposite fixes. An outstanding change file
    names the object: the database lags the code, and the fix is to apply that
    file. None does: the object exists nowhere, so the query is wrong or the
    change that adds it was never written — and saying "out of date" there
    would send the reader after a migration that does not exist.
    """
    def line(label: str, value: str) -> str:
        return f"    {label:<15} {value}"

    outstanding = _unapplied_changes(conn)
    named_by, adds_it = _changes_naming(missing, outstanding)
    lines = []
    if named_by:
        lines.append(
            f"endless database schema is out of date at {config.tilde(config.DB_PATH)}"
        )
        lines.append(line(f"missing {missing.kind}:", missing.label))
        for path in named_by:
            lines.append(line(
                "added by:" if adds_it else "named by:",
                f"{config.tilde(path)} — not applied to this database",
            ))
        lines.append(line(
            "apply it:" if adds_it else "try:",
            f"endless db apply-change {config.tilde(named_by[0])}",
        ))
    else:
        lines.append(
            f"endless database at {config.tilde(config.DB_PATH)} "
            f"has no {missing.kind} {missing.label}"
        )
        lines.append(
            "    the database is initialized; only this one object is absent"
        )
        if not _CHANGES_DIR.is_dir():
            lines.append(
                f"    no schema changes to check against: "
                f"{config.tilde(_CHANGES_DIR)} is not part of this install"
            )
        else:
            lines.append(
                f"    no outstanding schema change adds it "
                f"({len(outstanding)} checked in {config.tilde(_CHANGES_DIR)})"
            )
        lines.append(
            "    so either the query names it wrongly, or the change that adds "
            "it was never written"
        )
    lines.append(line("query:", _sql_excerpt(sql)))
    return click.ClickException("\n".join(lines))


def _missing_schema_hint() -> click.ClickException:
    """Build a ClickException explaining why the resolved DB has no schema.

    The user sees this when XDG_CONFIG_HOME points somewhere endless wasn't
    initialized (e.g., a sandbox subshell, a stale env override, or a worktree's
    own .endless/ — see E-1158, E-1162). Names the resolved path, the resolution
    mechanism, and the file's state so the user can spot the problem.
    """
    xdg = os.environ.get("XDG_CONFIG_HOME")
    if xdg:
        mechanism = f"resolved via XDG_CONFIG_HOME={xdg}"
        suggestion = ("    If unintentional: 'unset XDG_CONFIG_HOME' "
                      "(or 'exit' if you're in an `endless-go sandbox` subshell).")
    else:
        mechanism = "resolved via default ~/.config (XDG_CONFIG_HOME unset)"
        suggestion = ("    Initialize with 'endless project register <project-path>' "
                      "or check that you're invoking the expected endless install.")
    if config.DB_PATH.exists():
        try:
            size = config.DB_PATH.stat().st_size
            file_state = f"exists ({size} bytes) but has no endless schema"
        except OSError:
            file_state = "exists but cannot be stat'd"
    else:
        file_state = "does not exist"
    return click.ClickException(
        f"endless database is uninitialized at {config.DB_PATH}\n"
        f"    {mechanism}\n"
        f"    db file: {file_state}\n"
        f"{suggestion}"
    )


def execute(sql: str, params: tuple = ()) -> sqlite3.Cursor:
    db = get_db()
    try:
        cursor = db.execute(sql, params)
    except sqlite3.OperationalError as e:
        hint = _schema_error_hint(e, sql)
        if hint is not None:
            raise hint from e
        raise
    db.commit()
    return cursor


def query(sql: str, params: tuple = ()) -> list[sqlite3.Row]:
    try:
        return get_db().execute(sql, params).fetchall()
    except sqlite3.OperationalError as e:
        hint = _schema_error_hint(e, sql)
        if hint is not None:
            raise hint from e
        raise


def scalar(sql: str, params: tuple = ()):
    try:
        row = get_db().execute(sql, params).fetchone()
    except sqlite3.OperationalError as e:
        hint = _schema_error_hint(e, sql)
        if hint is not None:
            raise hint from e
        raise
    if row is None:
        return None
    return row[0]


def exists(sql: str, params: tuple = ()) -> bool:
    return scalar(sql, params) is not None
