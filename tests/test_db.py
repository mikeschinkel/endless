"""Tests for db module."""

from endless import db


def test_get_db_creates_tables(isolated_env):
    conn = db.get_db()
    tables = conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table'"
    ).fetchall()
    table_names = {row["name"] for row in tables}
    assert "projects" in table_names
    assert "notes" in table_names
    assert "sessions" in table_names


def test_execute_and_query(isolated_env):
    db.execute(
        "INSERT INTO projects "
        "(name, path, status, created_at, updated_at) "
        "VALUES (?, ?, ?, datetime('now'), datetime('now'))",
        ("test", "/tmp/test", "active"),
    )
    rows = db.query("SELECT name FROM projects")
    assert len(rows) == 1
    assert rows[0]["name"] == "test"


def test_scalar(isolated_env):
    db.execute(
        "INSERT INTO projects "
        "(name, path, status, created_at, updated_at) "
        "VALUES (?, ?, ?, datetime('now'), datetime('now'))",
        ("test", "/tmp/test", "active"),
    )
    count = db.scalar("SELECT count(*) FROM projects")
    assert count == 1


def test_exists(isolated_env):
    assert not db.exists("SELECT 1 FROM projects WHERE name='test'")

    db.execute(
        "INSERT INTO projects "
        "(name, path, status, created_at, updated_at) "
        "VALUES (?, ?, ?, datetime('now'), datetime('now'))",
        ("test", "/tmp/test", "active"),
    )
    assert db.exists("SELECT 1 FROM projects WHERE name='test'")
