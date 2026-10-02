"""SQLite database helpers."""

import os
import re
import sqlite3
from typing import NamedTuple

from endless import agent_help, config, event_bridge, provenance, statuses
from endless.config import ensure_config_dir

_conn: sqlite3.Connection | None = None

def get_db() -> sqlite3.Connection:
    global _conn
    # E-1429: refuse a direct DB read inside a self-dev worktree unless an
    # explicit --db was resolved. Choke point for the Python-side gate.
    config.require_db_context()
    # E-1668: and the choke point for saying which store answered. Marked here
    # rather than per command because this is the one place a direct read can
    # begin — a command cannot reach SQLite and forget to declare it.
    provenance.mark_touched()
    if _conn is not None:
        return _conn
    ensure_config_dir()
    is_new = not config.DB_PATH.exists()
    if is_new:
        # E-2019: Go owns the schema. The migration set is embedded in
        # endless-go, so this works from an installed tool as well as a source
        # checkout -- which reading internal/schema/schema.sql off disk never did.
        # Done BEFORE connecting, so the file opened below is already built.
        _init_schema()
    _conn = sqlite3.connect(str(config.DB_PATH))
    _conn.row_factory = sqlite3.Row
    # Decode TEXT leniently (E-1914). sqlite3's default text_factory raises
    # OperationalError on a column holding invalid UTF-8, and it raises for the
    # whole QUERY — one damaged byte anywhere in the result set takes down the
    # command, naming a column the user did not ask about.
    #
    # SQLite does not validate what it stores, so a writer that truncated a
    # string by BYTE length could leave a half-encoded codepoint behind. The
    # known instance was sessions.summary, written by the recap generator
    # removed in E-1906 (the column itself went in E-2074) — inert historical
    # damage no current code could add to, but present in every long-lived DB
    # and enough to crash `session list` and `session show` on the one row that
    # had it. The lenient decode stays: session_messages.content and the task
    # doc columns are TEXT written by the same class of writer.
    #
    # Applied at the connection, not per query, because the failure mode belongs
    # to reading TEXT at all: fixing it per-column is whack-a-mole against data
    # nothing can repair from here. U+FFFD marks the damage visibly instead of
    # hiding it.
    _conn.text_factory = lambda b: b.decode("utf-8", "replace")
    _conn.execute("PRAGMA journal_mode=WAL")
    _conn.execute("PRAGMA busy_timeout=5000")
    _conn.execute("PRAGMA foreign_keys=ON")
    if not is_new and not _has_table(_conn, "projects"):
        # File exists but lacks the foundational schema. Surface a clear error
        # naming the resolved path and resolution mechanism rather than a raw
        # OperationalError from the first query.
        #
        # Python migrates nothing (E-2158 deleted the legacy ladder that once
        # ran here): Go owns the schema, brings a database forward on its own
        # connect, and refuses one too old to bring forward at all.
        _conn.close()
        _conn = None
        raise _missing_schema_hint()
    return _conn


def _init_schema():
    """Create the database at the resolved DB context, at the latest version.

    Shells out to `endless-go event migrate`; Python applies no DDL of its own
    (E-2019). Called before the first connect, so the file sqlite3.connect()
    opens below is already built rather than empty.
    """
    event_bridge.init_schema()


def _has_table(conn: sqlite3.Connection, table: str) -> bool:
    row = conn.execute(
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?",
        (table,),
    ).fetchone()
    return row[0] > 0


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


def _sql_excerpt(sql: str, limit: int = 160) -> str:
    """The failing statement on one line, short enough to read."""
    one_line = " ".join(sql.split())
    if len(one_line) <= limit:
        return one_line
    return one_line[:limit - 1] + "…"


def _schema_error_hint(
    err: sqlite3.OperationalError, sql: str
) -> agent_help.Refusal | None:
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
    return _incomplete_schema_hint(missing, sql)


def _incomplete_schema_hint(missing: _MissingObject, sql: str) -> agent_help.Refusal:
    """Build the refusal for a schema'd database missing one table or column.

    The database has the endless schema, so it is not uninitialized: either it
    is at an older schema version than this endless expects, which `endless db
    upgrade` (E-2020) fixes, or the object exists nowhere and the query is what
    is wrong. The text names both, so the reader is not sent after a migration
    that does not exist.

    A fault either way. Every endless-go connect brings a behind database
    forward on its own (E-2020), so a database still missing an object by the
    time Python reads it means Endless is wrong about its own schema — there is
    no decision for the user and nothing for an agent to route around. (Until
    E-2158 a pending per-ticket script could add the object, and its
    sandbox-or-main split decided the class; goose has no pending files.)
    """
    def line(label: str, value: str) -> str:
        return f"    {label:<15} {value}"

    where = config.tilde(config.DB_PATH)
    text = "\n".join([
        f"endless database schema at {where} is older than this endless expects",
        line(f"missing {missing.kind}:", missing.label),
        line("upgrade it:", "endless db upgrade"),
        "    if it is already current, nothing adds this "
        f"{missing.kind}, so the query names it wrongly",
        line("query:", _sql_excerpt(sql)),
    ])
    summary = (
        f"The query needs {missing.kind} {missing.label}, which the database at "
        f"{where} does not have. The statement did not run."
    )
    return agent_help.fault(summary, text=text)


def _missing_schema_hint() -> agent_help.Refusal:
    """Build the refusal explaining why the resolved DB has no schema.

    The user sees this when XDG_CONFIG_HOME points somewhere endless wasn't
    initialized (e.g., a sandbox subshell, a stale env override, or a worktree's
    own .endless/ — see E-1158, E-1162). Names the resolved path, the resolution
    mechanism, and the file's state so the user can spot the problem.

    ENDLESS_SANDBOX decides the class, and it is read here rather than named to
    the agent as a question, because the environment variable IS the answer:

    - Set: this invocation runs inside an `endless-go sandbox` subshell, whose
      XDG_CONFIG_HOME redirected the lookup. NO-REPORT. Leaving the subshell
      fixes it, and the user never had a database at that path to have an
      opinion about.
    - Unset: this is the user's real config directory — XDG_CONFIG_HOME/endless
      when they set it, else ~/.config/endless (E-2186: a set XDG_CONFIG_HOME
      is the user's own choice, no longer a redirect Endless injected) —
      holding a file that exists and carries no schema. Whether to restore it
      from a backup or replace it is a judgement about their own data, and the
      suggestion the message has always printed cannot be followed by anyone —
      `project register` opens the same file through get_db and lands right
      back here.
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
    text = (
        f"endless database is uninitialized at {config.DB_PATH}\n"
        f"    {mechanism}\n"
        f"    db file: {file_state}\n"
        f"{suggestion}"
    )
    if os.environ.get("ENDLESS_SANDBOX"):
        return agent_help.no_report(
            f"The database at {config.DB_PATH} has no endless schema; the path "
            f"came from the `endless-go sandbox` subshell's "
            f"XDG_CONFIG_HOME={xdg}. Nothing was read or written.",
            "Re-run outside the sandbox: leave the `endless-go sandbox` "
            "subshell",
            text=text,
        )
    return agent_help.report(
        f"The database in the config directory, {config.DB_PATH}, "
        f"{file_state}. Nothing was read or written.",
        "whether to restore that database from a backup or start a new one — "
        "it is the file holding all of their Endless state",
        text=text,
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


def task_content(task_id: int) -> dict[str, str]:
    """Every content row of one task, name → content (E-1531).

    A task's plan, analysis, outcome, reason and notes are task_content rows,
    not columns. A name absent from the result is a name the task has none of:
    the table holds no empty rows.
    """
    return {
        r["name"]: r["content"]
        for r in query(
            "SELECT name, content FROM task_content WHERE task_id = ?", (task_id,)
        )
    }


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
