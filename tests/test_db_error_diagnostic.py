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
# Several of the cases below differ only in what the changes directory holds,
# and that is the whole point: the same sqlite3 error means "apply this file",
# "your query is wrong", or "this install ships no changes" depending on it.

OLD_HEADLINE = "endless database is uninitialized"


def _flat(message: str) -> str:
    """The message with its column alignment collapsed, so an assertion pins
    the wording rather than the padding."""
    return " ".join(message.split())


def _build_schemad_db_at(path, extra_sql: str = "") -> None:
    """A DB that IS an endless database — get_db() gates on `projects` — but
    whose `decisions` table lacks the column an unapplied change would add."""
    conn = sqlite3.connect(str(path))
    conn.executescript(
        "CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT);"
        "CREATE TABLE decisions (id INTEGER PRIMARY KEY, title TEXT);"
        "PRAGMA user_version = 6;"  # _migrate() short-circuits at >= 6
        + extra_sql
    )
    conn.commit()
    conn.close()


def _changes_dir(monkeypatch, tmp_path, **files: str) -> None:
    """Point db._CHANGES_DIR at a synthetic changes directory."""
    d = tmp_path / "changes"
    d.mkdir()
    for name, body in files.items():
        (d / name.replace("__", ".")).write_text(body)
    monkeypatch.setattr(db, "_CHANGES_DIR", d)


def test_missing_column_names_the_column_and_the_change_file(tmp_path, monkeypatch):
    """The E-2036 case: `decision show` selected a column an unapplied change
    adds, and got told the whole database was uninitialized."""
    stale_db = tmp_path / "stale.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)
    _changes_dir(
        monkeypatch, tmp_path,
        **{"e-1920-decision-end-states__sql":
           "ALTER TABLE decisions ADD COLUMN superseded_by INTEGER;"},
    )

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT d.superseded_by FROM decisions d")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "out of date" in msg
    assert "missing column: superseded_by" in msg
    assert "e-1920-decision-end-states.sql" in msg
    assert "endless db apply-change" in msg
    assert "XDG_CONFIG_HOME" not in msg


def test_applied_change_is_not_offered(tmp_path, monkeypatch):
    """A change already recorded in _schema_version is not the fix — offering
    it would send the reader to re-run a no-op."""
    stale_db = tmp_path / "applied.db"
    _build_schemad_db_at(
        stale_db,
        extra_sql=(
            "CREATE TABLE _schema_version (name TEXT PRIMARY KEY, applied_at TEXT);"
            "INSERT INTO _schema_version (name, applied_at) "
            "VALUES ('e-1920-decision-end-states', '2026-08-21T00:00:00');"
        ),
    )
    _swap_db_path(monkeypatch, stale_db)
    _changes_dir(
        monkeypatch, tmp_path,
        **{"e-1920-decision-end-states__sql":
           "ALTER TABLE decisions ADD COLUMN superseded_by INTEGER;"},
    )

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT superseded_by FROM decisions")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "out of date" not in msg
    assert "e-1920-decision-end-states.sql" not in msg
    assert "no outstanding schema change adds it" in msg


def test_no_change_adds_it_reads_as_a_query_bug(tmp_path, monkeypatch):
    """Nothing on disk adds the column, so "out of date" would be a lie — the
    message has to point at the query instead."""
    stale_db = tmp_path / "typo.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)
    _changes_dir(monkeypatch, tmp_path,
                 **{"e-1000-unrelated__sql": "ALTER TABLE projects ADD COLUMN label TEXT;"})

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT titel FROM decisions")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "has no column titel" in msg
    assert "no outstanding schema change adds it" in msg
    assert "query: SELECT titel FROM decisions" in msg


def test_changes_dir_absent_degrades_without_lying(tmp_path, monkeypatch):
    """PRODUCT: an install that is not a source checkout has no
    internal/schema/changes/ beside src/endless/. Diagnose what is knowable."""
    stale_db = tmp_path / "nochanges.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)
    monkeypatch.setattr(db, "_CHANGES_DIR", tmp_path / "not-installed")

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT superseded_by FROM decisions")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "has no column superseded_by" in msg
    assert "is not part of this install" in msg


def test_missing_table_on_a_schemad_db_is_diagnosed_too(tmp_path, monkeypatch):
    """A table an unapplied change creates gets the same treatment as a column."""
    stale_db = tmp_path / "notable.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)
    _changes_dir(
        monkeypatch, tmp_path,
        **{"e-1859-add-triage-claims__sql":
           "CREATE TABLE triage_claims (id INTEGER PRIMARY KEY);"},
    )

    with pytest.raises(click.ClickException) as exc:
        db.query("SELECT id FROM triage_claims")

    msg = _flat(exc.value.message)
    assert OLD_HEADLINE not in msg
    assert "missing table: triage_claims" in msg
    assert "e-1859-add-triage-claims.sql" in msg


def test_insert_into_a_missing_column_is_diagnosed(tmp_path, monkeypatch):
    """sqlite3 phrases an INSERT's missing column as "table T has no column
    named C" — a form the old prefix test missed entirely, so it surfaced as a
    bare OperationalError."""
    stale_db = tmp_path / "insert.db"
    _build_schemad_db_at(stale_db)
    _swap_db_path(monkeypatch, stale_db)
    _changes_dir(
        monkeypatch, tmp_path,
        **{"e-1920-decision-end-states__sql":
           "ALTER TABLE decisions ADD COLUMN superseded_by INTEGER;"},
    )

    with pytest.raises(click.ClickException) as exc:
        db.execute("INSERT INTO decisions (id, superseded_by) VALUES (1, 2)")

    msg = _flat(exc.value.message)
    assert "missing column: decisions.superseded_by" in msg
    assert "e-1920-decision-end-states.sql" in msg
