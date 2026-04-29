"""Tests for the general-purpose task relation CLI (E-957)."""

import sqlite3

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str, status: str = "ready") -> int:
    """Create a task directly in the DB; return its id."""
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type, phase, created_at) "
        "VALUES (1, ?, ?, 'task', 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _seed_project():
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', '/tmp/test', 'active', datetime('now'), datetime('now'))"
    )


def test_link_unlink_roundtrip_blocks(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")

    task_cmd.link_tasks(a, b, "blocks")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert len(rows) == 1
    # 'blocks' stores active-voice: source=A blocks target=B
    assert rows[0]["source_id"] == a
    assert rows[0]["target_id"] == b
    assert rows[0]["dep_type"] == "blocks"

    task_cmd.unlink_tasks(a, b, "blocks")
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_link_blocked_by_swaps(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")

    # "A blocked_by B" → stored as "B blocks A" (swap=True)
    task_cmd.link_tasks(a, b, "blocked_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == b
    assert rows[0]["target_id"] == a
    assert rows[0]["dep_type"] == "blocks"


def test_link_implements_no_swap(isolated_env):
    _seed_project()
    a = _add_task("Impl")
    d = _add_task("Decision")
    task_cmd.link_tasks(a, d, "implements")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == a
    assert rows[0]["target_id"] == d
    assert rows[0]["dep_type"] == "implements"


def test_link_relates_to_symmetric(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "relates_to")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["dep_type"] == "relates_to"


def test_self_link_rejected(isolated_env):
    _seed_project()
    a = _add_task("A")
    with pytest.raises(click.ClickException):
        task_cmd.link_tasks(a, a, "blocks")


def test_invalid_dep_type_rejected(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    with pytest.raises(click.ClickException):
        task_cmd.link_tasks(a, b, "fnord")


def test_unique_collision_friendly_error(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "blocks")
    with pytest.raises(click.ClickException) as exc_info:
        task_cmd.link_tasks(a, b, "blocks")
    assert "already linked" in str(exc_info.value)


def test_unlink_ambiguous_requires_as(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "blocks")
    task_cmd.link_tasks(a, b, "relates_to")
    with pytest.raises(click.ClickException) as exc_info:
        task_cmd.unlink_tasks(a, b)
    assert "Multiple relations" in str(exc_info.value) or "ambiguous" in str(exc_info.value).lower()


def test_unlink_unambiguous_no_as_works(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "relates_to")
    task_cmd.unlink_tasks(a, b)
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_unlink_no_relation_errors(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    with pytest.raises(click.ClickException):
        task_cmd.unlink_tasks(a, b)


def test_get_all_relations_groups_correctly(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    c = _add_task("C")

    task_cmd.link_tasks(a, b, "blocks")        # A blocks B
    task_cmd.link_tasks(c, a, "blocks")        # C blocks A → A blocked_by C
    task_cmd.link_tasks(a, b, "relates_to")    # symmetric

    rels = task_cmd.get_all_relations(a)
    assert "blocks" in rels
    assert "blocked_by" in rels
    assert "relates_to" in rels
    assert {r["id"] for r in rels["blocks"]} == {b}
    assert {r["id"] for r in rels["blocked_by"]} == {c}


def test_replace_task_active_voice(isolated_env):
    _seed_project()
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new)

    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    # Active-voice: "new replaces old" → source=new, target=old
    assert rows[0]["source_id"] == new
    assert rows[0]["target_id"] == old
    assert rows[0]["dep_type"] == "replaces"

    # Old should be obsolete
    status = db.scalar("SELECT status FROM tasks WHERE id = ?", (old,))
    assert status == "obsolete"


def test_related_task_ids_helper(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    c = _add_task("C")
    task_cmd.link_tasks(a, b, "blocks")
    task_cmd.link_tasks(c, a, "implements")

    # All relations
    ids = task_cmd._related_task_ids(a)
    assert set(ids) == {b, c}

    # Filtered by type
    ids = task_cmd._related_task_ids(a, "blocks")
    assert set(ids) == {b}
    ids = task_cmd._related_task_ids(a, "implemented_by")
    assert set(ids) == {c}


def test_migration_strips_check_and_swaps(tmp_path, monkeypatch):
    """Seed a CHECK-constrained legacy task_deps with 'needs' + 'replaces' rows; migration rewrites them."""
    from endless import config
    db_path = tmp_path / "legacy.db"
    monkeypatch.setattr(config, "CONFIG_DIR", tmp_path)
    monkeypatch.setattr(config, "DB_PATH", db_path)
    monkeypatch.setattr(db, "DB_PATH", db_path)
    monkeypatch.setattr(db, "_conn", None)

    # Pre-create with legacy schema (CHECK on dep_type) and legacy 'needs' rows.
    conn = sqlite3.connect(str(db_path))
    conn.executescript("""
        CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT, path TEXT, status TEXT, created_at TEXT, updated_at TEXT);
        CREATE TABLE tasks (
            id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, title TEXT,
            description TEXT, status TEXT NOT NULL DEFAULT 'needs_plan',
            type TEXT NOT NULL DEFAULT 'task', phase TEXT NOT NULL DEFAULT 'now',
            created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '',
            completed_at TEXT, sort_order INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE task_deps (
            id INTEGER PRIMARY KEY,
            source_type TEXT NOT NULL CHECK (source_type IN ('task', 'project')),
            source_id INTEGER NOT NULL,
            target_type TEXT NOT NULL CHECK (target_type IN ('task', 'project')),
            target_id INTEGER NOT NULL,
            dep_type TEXT NOT NULL DEFAULT 'blocks' CHECK (dep_type IN ('blocks', 'needs')),
            created_at TEXT NOT NULL DEFAULT '',
            UNIQUE(source_type, source_id, target_type, target_id)
        );
        INSERT INTO projects (id, name, path) VALUES (1, 'test', '/tmp');
        INSERT INTO tasks (id, project_id, title) VALUES (100, 1, 'A'), (200, 1, 'B');
        -- legacy passive layout: source=blocked, target=blocker
        INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
        VALUES ('task', 100, 'task', 200, 'needs');
    """)
    conn.commit()
    conn.close()

    # Trigger migration via get_db()
    new_conn = db.get_db()
    rows = [tuple(r) for r in new_conn.execute(
        "SELECT source_id, target_id, dep_type FROM task_deps ORDER BY id"
    )]
    sql = new_conn.execute(
        "SELECT sql FROM sqlite_master WHERE name='task_deps'"
    ).fetchone()[0]

    # CHECK should be gone; 'needs' should be 'blocks' with swap
    assert "CHECK" not in sql
    assert rows == [(200, 100, "blocks")]
