"""Tests for `endless agents` — the epic-scoped background-agent listing (E-1621).

The command shells out to `endless-go session-query list-bg-agents` (the DB read
is Go-side, per E-1486) and formats the JSON as a plain-text table. These tests
run under `seeded_project_at_cwd` so cwd resolves to a registered project and the
worktree `bin/endless-go` on PATH reads the same isolated DB the Python `db`
module seeds.
"""

from click.testing import CliRunner

from endless import db
from endless.cli import main


def _project_id(name: str = "test") -> int:
    return db.query("SELECT id FROM projects WHERE name = ?", (name,))[0]["id"]


def _add_task(project_id: int, title: str, task_type: str = "task") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (?, ?, 'ready', (SELECT id FROM task_types WHERE slug = ?), 'now', datetime('now'))",
        (project_id, title, task_type),
    )
    return cur.lastrowid


def _add_bg_agent(
    project_id: int,
    epic_id: int | None,
    task_id: int,
    *,
    kind: str = "background",
    state: str = "working",
    short_id: str | None = None,
    started: str = "2026-06-23T10:00:00",
) -> int:
    kind_id = 2 if kind == "background" else 1
    cur = db.execute(
        "INSERT INTO sessions "
        "(project_id, platform, state, active_task_id, active_epic_id, kind_id, short_id, started_at, last_activity) "
        "VALUES (?, 'claude', ?, ?, ?, ?, ?, ?, ?)",
        (project_id, state, task_id, epic_id, kind_id, short_id, started, started),
    )
    return cur.lastrowid


# ────────────────────────────────────────────────────────────────────────
# --epic
# ────────────────────────────────────────────────────────────────────────


def test_agents_epic_lists_working_bg_agents(seeded_project_at_cwd):
    pid = _project_id()
    epic = _add_task(pid, "Billing epic", "epic")
    child = _add_task(pid, "Wire the gateway")
    _add_bg_agent(pid, epic, child, short_id="aaa11111")

    runner = CliRunner()
    result = runner.invoke(main, ["agents", "--epic", f"E-{epic}"])
    assert result.exit_code == 0, result.output
    assert f"Background agents working under E-{epic} (1)" in result.output
    assert "aaa11111" in result.output
    assert f"E-{child}" in result.output
    assert "Wire the gateway" in result.output


def test_agents_epic_excludes_ended_tmux_and_other_epics(seeded_project_at_cwd):
    pid = _project_id()
    epic = _add_task(pid, "Target epic", "epic")
    other = _add_task(pid, "Other epic", "epic")
    c1 = _add_task(pid, "Live child")
    c2 = _add_task(pid, "Ended child")
    c3 = _add_task(pid, "Tmux child")
    c4 = _add_task(pid, "Other-epic child")
    _add_bg_agent(pid, epic, c1, short_id="live", started="2026-06-23T10:00:00")
    _add_bg_agent(pid, epic, c2, short_id="ended", state="ended")
    _add_bg_agent(pid, epic, c3, kind="tmux")  # not a bg agent
    _add_bg_agent(pid, other, c4, short_id="other")

    runner = CliRunner()
    result = runner.invoke(main, ["agents", "--epic", f"E-{epic}"])
    assert result.exit_code == 0, result.output
    assert f"(1)" in result.output
    assert "Live child" in result.output
    assert "Ended child" not in result.output
    assert "Tmux child" not in result.output
    assert "Other-epic child" not in result.output


def test_agents_epic_empty_state(seeded_project_at_cwd):
    pid = _project_id()
    epic = _add_task(pid, "Quiet epic", "epic")

    runner = CliRunner()
    result = runner.invoke(main, ["agents", "--epic", f"E-{epic}"])
    assert result.exit_code == 0, result.output
    assert f"No background agents working under E-{epic}." in result.output


# ────────────────────────────────────────────────────────────────────────
# --all
# ────────────────────────────────────────────────────────────────────────


def test_agents_all_lists_project_bg_agents_across_epics(seeded_project_at_cwd):
    pid = _project_id()
    e1 = _add_task(pid, "Epic one", "epic")
    e2 = _add_task(pid, "Epic two", "epic")
    c1 = _add_task(pid, "Child A")
    c2 = _add_task(pid, "Child B")
    _add_bg_agent(pid, e1, c1, short_id="a1")
    _add_bg_agent(pid, e2, c2, short_id="b2")

    runner = CliRunner()
    result = runner.invoke(main, ["agents", "--all"])
    assert result.exit_code == 0, result.output
    assert "Background agents working in this project (2)" in result.output
    assert "Child A" in result.output
    assert "Child B" in result.output


# ────────────────────────────────────────────────────────────────────────
# validation
# ────────────────────────────────────────────────────────────────────────


def test_agents_epic_and_all_is_an_error(seeded_project_at_cwd):
    runner = CliRunner()
    result = runner.invoke(main, ["agents", "--epic", "E-100", "--all"])
    assert result.exit_code != 0
    assert "not both" in result.output
