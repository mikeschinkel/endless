"""Tests for epic_cmd — the `endless epic` convenience surface (E-1540).

`endless epic` is a thin wrapper over the task machinery with type=epic
pinned. `epic add` / `epic update` go through the event path (the Go
`endless-go event` binary, exercised here via the bin/ on PATH the
isolated_env fixture sets up), so those tests run under
`seeded_project_at_cwd` where cwd resolves to a registered project.

`epic list` / `epic show` are read-only renderers — those tests insert
epic-typed rows directly and read them back, no event emission.
"""

import json

from click.testing import CliRunner

from endless import db
from endless.cli import main


def _seed_project(name: str = "test") -> int:
    cur = db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES (?, '/tmp/test', 'active', datetime('now'), datetime('now'))",
        (name,),
    )
    return cur.lastrowid


def _add_task(project_id: int, title: str = "T", task_type: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (?, ?, 'unplanned', (SELECT id FROM task_types WHERE slug = ?), 'now', datetime('now'))",
        (project_id, title, task_type),
    )
    return cur.lastrowid


def _project_id(name: str = "test") -> int:
    return db.query("SELECT id FROM projects WHERE name = ?", (name,))[0]["id"]


def _type_slug(item_id: int) -> str:
    row = db.query(
        "SELECT COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type "
        "FROM tasks WHERE id = ?",
        (item_id,),
    )
    return row[0]["type"]


# ────────────────────────────────────────────────────────────────────────
# epic add — event path
# ────────────────────────────────────────────────────────────────────────


def test_epic_add_creates_epic_typed_row(seeded_project_at_cwd):
    runner = CliRunner()
    result = runner.invoke(main, ["epic", "add", "Build the billing epic"])
    assert result.exit_code == 0, result.output
    rows = db.query("SELECT id FROM tasks WHERE title = 'Build the billing epic'")
    assert len(rows) == 1
    assert _type_slug(rows[0]["id"]) == "epic"


def test_epic_add_has_no_type_flag(seeded_project_at_cwd):
    """`epic add` pins type=epic — there is no --type option to override it."""
    runner = CliRunner()
    result = runner.invoke(
        main, ["epic", "add", "Build a thing", "--type", "task"]
    )
    assert result.exit_code != 0
    assert "--type" in result.output or "no such option" in result.output.lower()


# ────────────────────────────────────────────────────────────────────────
# epic list — filters to epic-typed rows
# ────────────────────────────────────────────────────────────────────────


def test_epic_list_empty(isolated_env):
    _seed_project()
    runner = CliRunner()
    result = runner.invoke(main, ["epic", "list", "--project", "test"])
    assert result.exit_code == 0, result.output
    assert "No tasks" in result.output


def test_epic_list_filters_to_epics_only(isolated_env):
    pid = _seed_project()
    eid = _add_task(pid, "An epic", task_type="epic")
    _add_task(pid, "A plain task", task_type="task")
    runner = CliRunner()
    result = runner.invoke(main, ["epic", "list", "--project", "test"])
    assert result.exit_code == 0, result.output
    assert f"E-{eid}" in result.output
    assert "An epic" in result.output
    assert "A plain task" not in result.output


def test_epic_list_json_roundtrips(isolated_env):
    pid = _seed_project()
    eid = _add_task(pid, "JSON epic", task_type="epic")
    _add_task(pid, "Non-epic", task_type="task")
    runner = CliRunner()
    result = runner.invoke(
        main, ["epic", "list", "--project", "test", "--json"]
    )
    assert result.exit_code == 0, result.output
    out = json.loads(result.output)
    assert len(out) == 1
    assert out[0]["id"] == f"E-{eid}"


# ────────────────────────────────────────────────────────────────────────
# epic show — children shown by default, hidden with --no-children
# ────────────────────────────────────────────────────────────────────────


def test_epic_show_includes_children_by_default(isolated_env):
    pid = _seed_project()
    eid = _add_task(pid, "Parent epic", task_type="epic")
    db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, parent_id, created_at) "
        "VALUES (?, 'Child task', 'unplanned', (SELECT id FROM task_types WHERE slug='task'), 'now', ?, datetime('now'))",
        (pid, eid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["epic", "show", f"E-{eid}"])
    assert result.exit_code == 0, result.output
    assert "Child task" in result.output


def test_epic_show_no_children_hides_them(isolated_env):
    pid = _seed_project()
    eid = _add_task(pid, "Parent epic", task_type="epic")
    db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, parent_id, created_at) "
        "VALUES (?, 'Child task', 'unplanned', (SELECT id FROM task_types WHERE slug='task'), 'now', ?, datetime('now'))",
        (pid, eid),
    )
    runner = CliRunner()
    result = runner.invoke(main, ["epic", "show", f"E-{eid}", "--no-children"])
    assert result.exit_code == 0, result.output
    assert "Child task" not in result.output


# ────────────────────────────────────────────────────────────────────────
# epic update — promotes type to epic + passes through non-type fields
# ────────────────────────────────────────────────────────────────────────


def test_epic_update_promotes_task_to_epic(seeded_project_at_cwd):
    pid = _project_id()
    tid = _add_task(pid, "Was a plain task", task_type="task")
    assert _type_slug(tid) == "task"
    runner = CliRunner()
    result = runner.invoke(
        main, ["epic", "update", f"E-{tid}", "--status", "ready"]
    )
    assert result.exit_code == 0, result.output
    assert _type_slug(tid) == "epic"


def test_epic_update_passes_through_non_type_fields(seeded_project_at_cwd):
    pid = _project_id()
    tid = _add_task(pid, "Some epic", task_type="epic")
    runner = CliRunner()
    result = runner.invoke(
        main,
        ["epic", "update", f"E-{tid}", "--phase", "next", "--description", "New desc"],
    )
    assert result.exit_code == 0, result.output
    row = db.query(
        "SELECT phase, description FROM tasks WHERE id = ?", (tid,)
    )[0]
    assert row["phase"] == "next"
    assert row["description"] == "New desc"
    assert _type_slug(tid) == "epic"


def test_epic_update_has_no_type_flag(seeded_project_at_cwd):
    pid = _project_id()
    tid = _add_task(pid, "Some epic", task_type="epic")
    runner = CliRunner()
    result = runner.invoke(
        main, ["epic", "update", f"E-{tid}", "--type", "task"]
    )
    assert result.exit_code != 0
    assert "--type" in result.output or "no such option" in result.output.lower()
