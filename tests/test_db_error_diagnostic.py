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
    assert "endless register" in msg


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
