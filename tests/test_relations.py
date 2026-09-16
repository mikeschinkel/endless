"""Tests for the general-purpose task relation CLI (E-957)."""

import sqlite3
from pathlib import Path

import click
import pytest

from endless import db, task_cmd


def _add_task(title: str, status: str = "ready") -> int:
    """Create a task directly in the DB; return its id."""
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _add_dep(source_id: int, target_id: int, dep_type: str):
    """Insert a task_deps row directly (STORED direction — no swap resolution).

    Fixture helper for tests whose subject is NOT the link/unlink write path
    (rendering, get_all_relations, _related_task_ids). Those now go through the
    Go event executor via emit_event; making every such test drive that stack
    would couple unrelated tests to the binary, session resolution, and git.
    This keeps them fast and isolated. Behavior tests that DO verify the write
    exercise the real emit path via link_tasks + _seed_project_at_cwd.
    """
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, ?)",
        (source_id, target_id, dep_type),
    )


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

    E-1206: also git-inits the project so the write-time auto-commit of
    db-ledger segments has a git work tree to commit against.
    """
    import subprocess
    proj_dir = isolated_env["projects_root"]
    monkeypatch.chdir(proj_dir)

    def _git(*args):
        subprocess.run(["git", *args], cwd=str(proj_dir), check=True,
                       capture_output=True)

    _git("init", "-q", "-b", "main")
    _git("config", "user.email", "test@example.com")
    _git("config", "user.name", "Test")
    _git("commit", "--allow-empty", "-q", "-m", "initial")

    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
    )


def test_link_unlink_roundtrip_blocks(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
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


def test_link_blocked_by_swaps(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a = _add_task("A")
    b = _add_task("B")

    # "A blocked_by B" → stored as "B blocks A" (swap=True)
    task_cmd.link_tasks(a, b, "blocked_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == b
    assert rows[0]["target_id"] == a
    assert rows[0]["dep_type"] == "blocks"


def test_link_implements_no_swap(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a = _add_task("Impl")
    d = _add_task("Decision")
    task_cmd.link_tasks(a, d, "implements")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == a
    assert rows[0]["target_id"] == d
    assert rows[0]["dep_type"] == "implements"


def test_link_relates_to_symmetric(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "relates_to")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["dep_type"] == "relates_to"


def test_link_cleans_up_no_swap(isolated_env, monkeypatch):
    """E-1145: 'cleans_up' stores active-voice; source is the cleanup task."""
    _seed_project_at_cwd(monkeypatch, isolated_env)
    cleanup = _add_task("Retype prose links")
    parent = _add_task("Parent that shipped")
    task_cmd.link_tasks(cleanup, parent, "cleans_up")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == cleanup
    assert rows[0]["target_id"] == parent
    assert rows[0]["dep_type"] == "cleans_up"


def test_link_cleaned_up_by_swaps(isolated_env, monkeypatch):
    """E-1145: 'cleaned_up_by' is the inverse view, swaps source and target."""
    _seed_project_at_cwd(monkeypatch, isolated_env)
    parent = _add_task("Parent that shipped")
    cleanup = _add_task("Retype prose links")
    # "parent cleaned_up_by cleanup" → stored as "cleanup cleans_up parent"
    task_cmd.link_tasks(parent, cleanup, "cleaned_up_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == cleanup
    assert rows[0]["target_id"] == parent
    assert rows[0]["dep_type"] == "cleans_up"


def test_unlink_cleans_up(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
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

    _seed_project_at_cwd(monkeypatch, isolated_env)
    parent = _add_task("Add feature flag")

    def _stub(title, description=None, text=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False, **kwargs):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type_id, phase, created_at) "
            "VALUES (1, ?, ?, ?, (SELECT id FROM task_types WHERE slug = ?), ?, datetime('now'))",
            (title, description or "", status or "unplanned", task_type or "todo", phase),
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

    _seed_project_at_cwd(monkeypatch, isolated_env)
    cleanup_task = _add_task("Existing cleanup task")

    def _stub(title, description=None, text=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False, **kwargs):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type_id, phase, created_at) "
            "VALUES (1, ?, ?, ?, (SELECT id FROM task_types WHERE slug = ?), ?, datetime('now'))",
            (title, description or "", status or "unplanned", task_type or "todo", phase),
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


def test_unique_collision_friendly_error(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "blocks")
    # The friendly duplicate message is raised by the Python pre-check (the Go
    # UNIQUE constraint is only the low-level integrity backstop).
    with pytest.raises(click.ClickException) as exc_info:
        task_cmd.link_tasks(a, b, "blocks")
    assert "already linked" in str(exc_info.value)


def test_unlink_ambiguous_requires_as(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a = _add_task("A")
    b = _add_task("B")
    task_cmd.link_tasks(a, b, "blocks")
    task_cmd.link_tasks(a, b, "relates_to")
    with pytest.raises(click.ClickException) as exc_info:
        task_cmd.unlink_tasks(a, b)
    assert "Multiple relations" in str(exc_info.value) or "ambiguous" in str(exc_info.value).lower()


def test_unlink_unambiguous_no_as_works(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
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

    _add_dep(a, b, "blocks")        # A blocks B
    _add_dep(c, a, "blocks")        # C blocks A → A blocked_by C
    _add_dep(a, b, "relates_to")    # symmetric

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

    # Old should be superseded
    status = db.scalar("SELECT status FROM tasks WHERE id = ?", (old,))
    assert status == "superseded"


def test_related_task_ids_helper(isolated_env):
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    c = _add_task("C")
    _add_dep(a, b, "blocks")
    _add_dep(c, a, "implements")

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
    # db.py reads config.DB_PATH dynamically (E-1429); no db.DB_PATH to patch.
    monkeypatch.setattr(db, "_conn", None)

    # Pre-create with legacy schema (CHECK on dep_type) and legacy 'needs' rows.
    conn = sqlite3.connect(str(db_path))
    conn.executescript("""
        CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT, path TEXT, status TEXT, created_at TEXT, updated_at TEXT);
        CREATE TABLE tasks (
            id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, title TEXT,
            description TEXT, status TEXT NOT NULL DEFAULT 'unplanned',
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


# --- E-1477: unified "Links:" rendering ---------------------------------------


def _invoke(*args):
    """Run the endless CLI under CliRunner; assert success and return output."""
    from click.testing import CliRunner

    from endless.cli import main

    result = CliRunner().invoke(main, list(args))
    assert result.exit_code == 0, result.output
    return result.output


def test_flatten_relations_id_ascending_directional(isolated_env):
    """E-1477: relations flatten into one id-ascending list with lower-cased
    directional labels."""
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    c = _add_task("C")

    _add_dep(a, b, "blocks")        # A blocks B
    _add_dep(c, a, "blocks")        # C blocks A → A blocked_by C
    _add_dep(a, b, "relates_to")    # symmetric

    flat = task_cmd._flatten_relations(a)
    ids = [r["id"] for r in flat]
    assert ids == sorted(ids)                  # id-ascending
    pairs = {(r["id"], r["rel"]) for r in flat}
    assert (b, "blocks") in pairs
    assert (b, "relates to") in pairs
    assert (c, "blocked by") in pairs
    # E-1576: each entry also carries the proper-cased directional phrase
    labels = {(r["id"], r["rel_label"]) for r in flat}
    assert (c, "Blocked by") in labels


def test_task_show_renders_links_section(isolated_env):
    """E-1477: `task show` renders one Links: section (id, type, status; titles
    omitted), placed below Created:."""
    _seed_project()
    a = _add_task("A", status="ready")
    b = _add_task("Distinctive beta title", status="ready")
    c = _add_task("Distinctive gamma title", status="confirmed")
    _add_dep(a, b, "blocks")        # A blocks B
    _add_dep(c, a, "blocks")        # A blocked_by C

    out = _invoke("task", "show", str(a))
    assert "This task:" in out                      # E-1576: subject heading
    assert f"Blocks:" in out and f"E-{b} [ready]" in out
    assert f"Blocked by:" in out and f"E-{c} [confirmed]" in out
    assert "Distinctive beta title" not in out      # titles omitted from rows
    assert "Distinctive gamma title" not in out
    assert "(blocks)" not in out                    # old parenthetical format gone
    assert "(blocked by)" not in out
    assert out.index("Created:") < out.index("This task:")  # multi-line block sits last


def test_task_show_agent_links_line(isolated_env):
    """E-1477: `task show --agent` emits a single compact links= line."""
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    _add_dep(a, b, "blocks")

    out = _invoke("task", "show", str(a), "--agent")
    assert f"links=E-{b} (blocks)" in out
    assert "blocked_by=" not in out            # old per-type key gone
    assert "blocks=" not in out


def test_task_relations_renders_links_section(isolated_env):
    """E-1477: `task relations` renders one ungrouped Links: section (titles omitted)."""
    _seed_project()
    a = _add_task("A", status="ready")
    b = _add_task("Distinctive beta title", status="ready")
    _add_dep(a, b, "blocks")
    _add_dep(a, b, "relates_to")

    out = _invoke("task", "relations", str(a))
    assert "This task:" in out                  # E-1576: subject heading
    assert "Blocks:" in out
    assert "Relates to:" in out
    assert f"E-{b} [ready]" in out
    assert "Distinctive beta title" not in out  # title omitted
    assert "(blocks)" not in out                # old parenthetical format gone
    assert "(relates to)" not in out


def test_task_relations_agent_links_line(isolated_env):
    """E-1477: `task relations --agent` emits a single Links: line."""
    _seed_project()
    a = _add_task("A")
    b = _add_task("B")
    _add_dep(a, b, "relates_to")

    out = _invoke("task", "relations", str(a), "--agent")
    assert f"Links: E-{b} (relates to)" in out


def test_task_relations_none(isolated_env):
    """E-1477: a task with no relations still reports (none)."""
    _seed_project()
    a = _add_task("A")
    out = _invoke("task", "relations", str(a))
    assert "(none)" in out


# ── duplicates / duplicated_by (E-1185) ──────────────────────────────────────


def test_link_duplicates_no_swap(isolated_env, monkeypatch):
    """E-1185: 'duplicates' stores active-voice; source is the redundant filing."""
    _seed_project_at_cwd(monkeypatch, isolated_env)
    dupe = _add_task("Add extensions/worktree-init.sh hook")
    keeper = _add_task("Add post-worktree-create.sh hook")
    task_cmd.link_tasks(dupe, keeper, "duplicates")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == dupe
    assert rows[0]["target_id"] == keeper
    assert rows[0]["dep_type"] == "duplicates"


def test_link_duplicated_by_swaps(isolated_env, monkeypatch):
    """E-1185: 'duplicated_by' is the inverse view — same row, written from the
    keeper's side."""
    _seed_project_at_cwd(monkeypatch, isolated_env)
    keeper = _add_task("Add post-worktree-create.sh hook")
    dupe = _add_task("Add extensions/worktree-init.sh hook")
    # "keeper duplicated_by dupe" → stored as "dupe duplicates keeper"
    task_cmd.link_tasks(keeper, dupe, "duplicated_by")
    rows = list(db.query("SELECT source_id, target_id, dep_type FROM task_deps"))
    assert rows[0]["source_id"] == dupe
    assert rows[0]["target_id"] == keeper
    assert rows[0]["dep_type"] == "duplicates"


def test_unlink_duplicates(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    dupe = _add_task("Filed twice")
    keeper = _add_task("Filed first")
    task_cmd.link_tasks(dupe, keeper, "duplicates")
    task_cmd.unlink_tasks(dupe, keeper, "duplicates")
    rows = list(db.query("SELECT * FROM task_deps"))
    assert rows == []


def test_duplicates_in_canonical_registries():
    """E-1185: registries expose both directions and the stored type."""
    assert task_cmd.CANONICAL_DEP_TYPES["duplicates"] == ("duplicates", False)
    assert task_cmd.CANONICAL_DEP_TYPES["duplicated_by"] == ("duplicates", True)
    assert "duplicates" in task_cmd.STORED_DEP_TYPES
    assert "duplicates" in task_cmd.RELATION_DISPLAY_ORDER
    assert "duplicated_by" in task_cmd.RELATION_DISPLAY_ORDER
    assert task_cmd.RELATION_LABELS["duplicates"] == "Duplicates"
    assert task_cmd.RELATION_LABELS["duplicated_by"] == "Duplicated by"


def test_duplicates_is_its_own_stored_type():
    """E-1185: the whole point — a duplicate is not a `replaces` and not a
    `relates_to`. Storing it as either loses the fact."""
    assert task_cmd.CANONICAL_DEP_TYPES["duplicates"][0] not in (
        "replaces", "relates_to",
    )
    assert task_cmd.STORED_DEP_TYPES.count("duplicates") == 1


def test_duplicates_display_order_sits_with_replaces():
    """E-1185: adjacent to `replaces` — the two are told apart by reading them
    side by side, and the order is what puts them there."""
    order = task_cmd.RELATION_DISPLAY_ORDER
    assert order.index("duplicates") == order.index("replaced_by") + 1
    assert order.index("duplicated_by") == order.index("duplicates") + 1


def test_duplicates_renders_in_links_section(isolated_env):
    """E-1185: both directions render with their own heading in `task show`."""
    _seed_project()
    dupe = _add_task("Filed twice", status="ready")
    keeper = _add_task("Filed first", status="ready")
    _add_dep(dupe, keeper, "duplicates")

    assert "Duplicates:" in _invoke("task", "relations", str(dupe))
    assert "Duplicated by:" in _invoke("task", "relations", str(keeper))


def test_duplicates_agent_line(isolated_env):
    """E-1185: the --agent line carries the directional phrase, not a raw type."""
    _seed_project()
    dupe = _add_task("Filed twice")
    keeper = _add_task("Filed first")
    _add_dep(dupe, keeper, "duplicates")

    assert f"Links: E-{keeper} (duplicates)" in _invoke(
        "task", "relations", str(dupe), "--agent")
    assert f"Links: E-{dupe} (duplicated by)" in _invoke(
        "task", "relations", str(keeper), "--agent")


def test_task_link_help_lists_duplicates():
    """E-1185: `task link --help` names both directions — the surface the task
    title calls out. An agent that cannot see the type will not use it."""
    out = _invoke("task", "link", "--help")
    assert "duplicates" in out
    assert "duplicated_by" in out


def test_duplicates_rejected_toward_a_decision(isolated_env):
    """E-1185: task→decision keeps its own narrower vocabulary; `duplicates` is
    a task↔task relation and must not leak into it."""
    from click.testing import CliRunner

    from endless.cli import main

    _seed_project()
    a = _add_task("A")
    db.execute(
        "INSERT INTO decisions (id, project_id, title, status, created_at) "
        "VALUES (9, 1, 'A decision', 'accepted', datetime('now'))"
    )
    result = CliRunner().invoke(
        main, ["task", "link", str(a), "--to", "ED-9", "--type", "duplicates"])
    assert result.exit_code != 0
    assert "not legal for task→decision" in result.output
