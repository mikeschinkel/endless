"""Tests for the general-purpose task relation CLI (E-957)."""

import sqlite3
from pathlib import Path

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


def _seed_project_at_cwd(monkeypatch, isolated_env):
    """Seed a project AT pytest tmp_path and chdir there.

    Required for tests that call functions emitting events (e.g. replace_task),
    because _resolve_project(None) inspects cwd. The default cwd (the endless
    repo) has a .endless/config.json that resolves to a name not present in the
    test DB, so we chdir to a clean tmp dir and seed the project at that path.
    """
    proj_dir = isolated_env["projects_root"]
    monkeypatch.chdir(proj_dir)
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
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


def test_link_cleans_up_no_swap(isolated_env):
    """E-1145: 'cleans_up' stores active-voice; source is the cleanup task."""
    _seed_project()
    cleanup = _add_task("Retype prose links")
    parent = _add_task("Parent that shipped")
    task_cmd.link_tasks(cleanup, parent, "cleans_up")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == cleanup
    assert rows[0]["target_id"] == parent
    assert rows[0]["dep_type"] == "cleans_up"


def test_link_cleaned_up_by_swaps(isolated_env):
    """E-1145: 'cleaned_up_by' is the inverse view, swaps source and target."""
    _seed_project()
    parent = _add_task("Parent that shipped")
    cleanup = _add_task("Retype prose links")
    # "parent cleaned_up_by cleanup" → stored as "cleanup cleans_up parent"
    task_cmd.link_tasks(parent, cleanup, "cleaned_up_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == cleanup
    assert rows[0]["target_id"] == parent
    assert rows[0]["dep_type"] == "cleans_up"


def test_unlink_cleans_up(isolated_env):
    _seed_project()
    cleanup = _add_task("Retype prose links")
    parent = _add_task("Parent that shipped")
    task_cmd.link_tasks(cleanup, parent, "cleans_up")
    task_cmd.unlink_tasks(cleanup, parent, "cleans_up")
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_cleans_up_in_canonical_registries():
    """E-1145: registries expose both directions and the stored type."""
    assert "cleans_up" in task_cmd.CANONICAL_DEP_TYPES
    assert "cleaned_up_by" in task_cmd.CANONICAL_DEP_TYPES
    assert task_cmd.CANONICAL_DEP_TYPES["cleans_up"] == ("cleans_up", False)
    assert task_cmd.CANONICAL_DEP_TYPES["cleaned_up_by"] == ("cleans_up", True)
    assert "cleans_up" in task_cmd.STORED_DEP_TYPES
    assert "cleans_up" in task_cmd.RELATION_DISPLAY_ORDER
    assert "cleaned_up_by" in task_cmd.RELATION_DISPLAY_ORDER
    assert task_cmd.RELATION_LABELS["cleans_up"] == "Cleans up"
    assert task_cmd.RELATION_LABELS["cleaned_up_by"] == "Cleaned up by"


def test_task_add_cleans_up_flag(isolated_env, monkeypatch):
    """E-1145: 'task add --cleans-up <id>' wires the new task as cleanup of <id>."""
    from click.testing import CliRunner

    from endless.cli import main

    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )
    parent = _add_task("Add feature flag")

    def _stub(title, description=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
            "VALUES (1, ?, ?, ?, ?, ?, datetime('now'))",
            (title, description or "", status or "needs_plan", task_type or "task", phase),
        )
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Remove feature flag after rampup",
        "--cleans-up", str(parent),
    ])
    assert result.exit_code == 0, result.output

    rows = list(db.query(
        "SELECT source_id, target_id, dep_type FROM task_deps "
        "WHERE dep_type = 'cleans_up'"
    ))
    assert len(rows) == 1
    assert rows[0]["target_id"] == parent
    assert rows[0]["source_id"] != parent  # the new task


def test_task_add_cleaned_up_by_flag(isolated_env, monkeypatch):
    """E-1145: 'task add --cleaned-up-by <id>' swaps to the canonical direction."""
    from click.testing import CliRunner

    from endless.cli import main

    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('sample', '/tmp/sample', 'active', datetime('now'), datetime('now'))"
    )
    cleanup_task = _add_task("Existing cleanup task")

    def _stub(title, description=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type, phase, created_at) "
            "VALUES (1, ?, ?, ?, ?, ?, datetime('now'))",
            (title, description or "", status or "needs_plan", task_type or "task", phase),
        )
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)

    runner = CliRunner()
    result = runner.invoke(main, [
        "task", "add", "Add feature flag",
        "--cleaned-up-by", str(cleanup_task),
    ])
    assert result.exit_code == 0, result.output

    rows = list(db.query(
        "SELECT source_id, target_id, dep_type FROM task_deps "
        "WHERE dep_type = 'cleans_up'"
    ))
    assert len(rows) == 1
    # canonical: source = cleanup task, target = parent (the new task)
    assert rows[0]["source_id"] == cleanup_task
    assert rows[0]["target_id"] != cleanup_task


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


def test_replace_task_active_voice(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
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
