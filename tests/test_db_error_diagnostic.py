"""Tests for the missing-schema diagnostic in db.py (E-1160)."""

import sqlite3

import click
import pytest

from endless import config, db


def _build_empty_db_at(path) -> None:
    """Create a SQLite file at path with no tables — mimics the state when
    XDG_CONFIG_HOME points somewhere endless wasn't initialized."""
    conn = sqlite3.connect(str(path))
    conn.close()


def _swap_db_path(monkeypatch, new_path) -> None:
    """Point config.DB_PATH at new_path and reset the cached connection.

    db.py reads config.DB_PATH dynamically (E-1429), so patching the config
    module is what redirects get_db(). Closes any currently-open connection
    first so the test doesn't leak file descriptors across the suite (macOS
    default ulimit -n is 256; the autouse isolated_env fixture already opens
    one per test).
    """
    if db._conn is not None:
        try:
            db._conn.close()
        except sqlite3.Error:
            pass
    monkeypatch.setattr(config, "DB_PATH", new_path)
    monkeypatch.setattr(db, "_conn", None)


def test_missing_table_raises_click_exception(tmp_path, monkeypatch):
    empty_db = tmp_path / "empty.db"
    _build_empty_db_at(empty_db)
    _swap_db_path(monkeypatch, empty_db)

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT id FROM tasks")

    msg = exc.value.message
    assert "uninitialized" in msg
    assert str(empty_db) in msg
    assert "exists" in msg  # file_state line


def test_diagnostic_names_xdg_config_home_when_set(tmp_path, monkeypatch):
    empty_db = tmp_path / "empty.db"
    _build_empty_db_at(empty_db)
    _swap_db_path(monkeypatch, empty_db)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "fake-xdg"))

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT id FROM tasks")

    msg = exc.value.message
    assert "XDG_CONFIG_HOME" in msg
    assert str(tmp_path / "fake-xdg") in msg
    assert "unset XDG_CONFIG_HOME" in msg


def test_diagnostic_when_xdg_unset(tmp_path, monkeypatch):
    empty_db = tmp_path / "empty.db"
    _build_empty_db_at(empty_db)
    _swap_db_path(monkeypatch, empty_db)
    monkeypatch.delenv("XDG_CONFIG_HOME", raising=False)

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT id FROM tasks")

    msg = exc.value.message
    assert "XDG_CONFIG_HOME unset" in msg
    assert "endless project register" in msg


def test_non_schema_operational_error_passes_through(isolated_env):
    """Other sqlite3.OperationalErrors (syntax errors, etc.) must NOT be
    swallowed by the diagnostic — they're real bugs to surface. Uses
    isolated_env so the DB is fully initialized; the error comes from a
    bad SQL statement, not from missing tables."""
    with pytest.raises(sqlite3.OperationalError):
        db.query("SELEKT bad syntax FROM projects")


def test_scalar_and_execute_also_diagnose(tmp_path, monkeypatch):
    empty_db = tmp_path / "empty.db"
    _build_empty_db_at(empty_db)
    _swap_db_path(monkeypatch, empty_db)

    with pytest.raises(click.ClickException):
        db.scalar("SELECT count(*) FROM tasks")

    _swap_db_path(monkeypatch, empty_db)
    with pytest.raises(click.ClickException):
        db.execute("DELETE FROM tasks WHERE id = 1")


# --- E-2036: a missing column is not an uninitialized database ---------------
#
# A database that has the endless schema and lacks one object is at an older
# schema version than this endless expects. Since E-2158 the fix it names is
# `endless db upgrade` (E-2020); the per-ticket script lookup it used to do went
# with those scripts.

OLD_HEADLINE = "endless database is uninitialized"


def _flat(message: str) -> str:
    """The message with its column alignment collapsed, so an assertion pins
    the wording rather than the padding."""
    return " ".join(message.split())


def _build_schemad_db_at(path, extra_sql: str = "") -> None:
    """A DB that IS an endless database — get_db() gates on `projects` — but
    whose `decisions` table lacks a column a newer schema would have."""
    conn = sqlite3.connect(str(path))
    conn.executescript(
        "CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT);"
        "CREATE TABLE decisions (id INTEGER PRIMARY KEY, title TEXT);"
        + extra_sql
    )
    conn.commit()
    conn.close()


def test_missing_column_names_the_column_and_db_upgrade(tmp_path, monkeypatch):
    """The E-2036 case: `decision show` selected a column the database lacks,
    and got told the whole database was uninitialized."""
    stale_db = tmp_path / "stale.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT d.superseded_by FROM decisions d")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "older than this endless expects" in msg
    assert "missing column: superseded_by" in msg
    assert "endless db upgrade" in msg
    assert "XDG_CONFIG_HOME" not in msg
    assert "query: SELECT d.superseded_by FROM decisions d" in msg


def test_missing_table_on_a_schemad_db_is_diagnosed_too(tmp_path, monkeypatch):
    """A missing table gets the same treatment as a missing column."""
    stale_db = tmp_path / "notable.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT id FROM task_questions")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "missing table: task_questions" in msg
    assert "endless db upgrade" in msg


def test_insert_into_a_missing_column_is_diagnosed(tmp_path, monkeypatch):
    """sqlite3 phrases an INSERT's missing column as "table T has no column
    named C" — a form the old prefix test missed entirely, so it surfaced as a
    bare OperationalError."""
    stale_db = tmp_path / "insert.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)

    with pytest.raises(click.ClickException) as exc:
        db.execute("INSERT INTO decisions (id, superseded_by) VALUES (1, 2)")

    msg = _flat(exc.value.message)
    assert "missing column: decisions.superseded_by" in msg
    assert "endless db upgrade" in msg


# --- E-2186: a set XDG_CONFIG_HOME is the user's own config dir --------------

def test_user_set_xdg_is_reported_not_waved_off(tmp_path, monkeypatch):
    """XDG_CONFIG_HOME is the user's own setting, so a schemaless database
    under it is their real data: REPORT. Only the `endless-go sandbox`
    subshell's redirect is something the agent may fix by leaving it."""
    from endless import agent_help

    empty_db = tmp_path / "empty.db"
    _build_empty_db_at(empty_db)
    _swap_db_path(monkeypatch, empty_db)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "user-xdg"))
    monkeypatch.delenv("ENDLESS_SANDBOX", raising=False)

    with pytest.raises(agent_help.Refusal) as exc:
        db.query("SELECT id FROM tasks")
    assert exc.value.cls == agent_help.REPORT

    _swap_db_path(monkeypatch, empty_db)
    monkeypatch.setenv("ENDLESS_SANDBOX", str(tmp_path / "sb"))
    with pytest.raises(agent_help.Refusal) as exc:
        db.query("SELECT id FROM tasks")
    assert exc.value.cls == agent_help.NO_REPORT



# --- E-2158: Python connect runs no migration ladder -------------------------


def test_python_read_does_not_modify_a_user_version_zero_db(tmp_path, monkeypatch):
    """Every database Go builds sits at PRAGMA user_version 0, which the
    retired Python ladder read as "take every step, after a backup". A read
    must leave such a database exactly as it found it: same objects, same
    user_version, and no backup directory beside it."""
    plain_db = tmp_path / "plain.db"
    _build_schemad_db_at(plain_db)
    _swap_db_path(monkeypatch, plain_db)

    def snapshot():
        conn = sqlite3.connect(str(plain_db))
        try:
            objects = conn.execute(
                "SELECT type, name, sql FROM sqlite_master ORDER BY type, name"
            ).fetchall()
            version = conn.execute("PRAGMA user_version").fetchone()[0]
        finally:
            conn.close()
        return objects, version

    before = snapshot()
    assert before[1] == 0
    assert db.query("SELECT id FROM decisions") == []
    db._conn.close()
    monkeypatch.setattr(db, "_conn", None)

    assert snapshot() == before
    assert not (tmp_path / "backups").exists()
