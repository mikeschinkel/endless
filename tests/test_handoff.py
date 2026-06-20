"""Tests for the generated spawn handoff (E-1469, E-1565, E-1566).

The handoff is rendered from per-type embedded templates under
`templates/handoff/{task,bug,research,epic}.md.tmpl` (E-1566), merged
with the task's id/title and runtime context (spawning pane, spawning
session's task). E-1565 moved rendering from Python string.Template to a
shell-out to `endless-go template render`, so the test chdirs into a tmp
project (one with a `.endless/` subdir) so the renderer can resolve a
project context.
"""

import pytest

from endless import db
from endless.task_cmd import render_handoff


@pytest.fixture(autouse=True)
def chdir_to_handoff_project(tmp_path, monkeypatch):
    """Renderer resolves project root from cwd; give it a `.endless/` dir."""
    (tmp_path / ".endless").mkdir()
    monkeypatch.chdir(tmp_path)


def _seed_project_and_parent(parent_id: int, child_count: int = 0) -> None:
    """Insert a project + parent task + N child tasks. Used to drive the
    child_count lookup inside render_handoff."""
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('handoff-test', '/tmp/handoff-test', 'active', "
        "datetime('now'), datetime('now'))",
    )
    pid = db.query("SELECT id FROM projects WHERE name = 'handoff-test'")[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (parent_id, pid, "parent", "in_progress"),
    )
    for i in range(child_count):
        db.execute(
            "INSERT INTO tasks (project_id, parent_id, title, status) "
            "VALUES (?, ?, ?, ?)",
            (pid, parent_id, f"child {i}", "ready"),
        )


def test_render_handoff_includes_task_and_return_path():
    out = render_handoff(
        spawned_id=1469,
        title="Render handoff from template",
        return_anchor="%7",
        spawner_task_id=1400,
        worktree_path="/repo/.endless/worktrees/e-1469",
        branch="task/1469-render-handoff",
        task_type="task",
    )
    assert "E-1469" in out
    assert "Render handoff from template" in out
    # Return path names the spawning pane verbatim.
    assert "tmux switch-client -t %7" in out
    # Origin line names the spawning session's task.
    assert "E-1400" in out
    # Worktree + branch substituted.
    assert "/repo/.endless/worktrees/e-1469" in out
    assert "task/1469-render-handoff" in out
    # Delegates the workflow to the guide and points at the plan.
    assert "endless guide" in out
    assert "endless task show E-1469 --text" in out
    # Generic handoff rules that apply to every spawn.
    assert "STOP and ask" in out
    assert "Don't mark `confirmed`/`assumed`" in out


def test_render_handoff_degrades_without_runtime_context():
    out = render_handoff(
        spawned_id=1469,
        title="t",
        return_anchor=None,
        spawner_task_id=None,
        task_type=None,
    )
    # Still renders; missing context becomes visible placeholders rather
    # than crashing or leaving a blank.
    assert "E-1469" in out
    assert "E-?" in out
    assert "%<spawning-pane>" in out
    assert "<task worktree>" in out
    assert "<task branch>" in out


def test_render_handoff_bug_variant():
    out = render_handoff(
        spawned_id=2000,
        title="Crash on empty input",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="bug",
    )
    # Bug-specific framing.
    assert "Reproduce the bug first" in out
    # Bug still goes to verify like a task.
    assert "--status verify" in out


def test_render_handoff_research_variant():
    out = render_handoff(
        spawned_id=2001,
        title="Survey caching strategies",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="research",
    )
    # Research-specific framing.
    assert "Findings are the deliverable" in out
    # End state guidance points at completed + outcome (file form, E-1001).
    assert "--status completed --outcome-file" in out
    # Research must NOT instruct --status verify (its own gate per ED-1502).
    assert "--status verify" not in out


def test_render_handoff_epic_variant():
    out = render_handoff(
        spawned_id=2002,
        title="Migrate ingestion pipeline",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="epic",
    )
    # Epic-specific framing.
    assert "coordinator" in out
    assert "draft plans" in out
    # Step 3 points at children.
    assert "--children" in out
    # Step 6 points at epic completion.
    assert "--status completed" in out
    # Epics never go to verify (children do their own verification).
    assert "--status verify" not in out


def test_render_handoff_unknown_type_falls_back_to_task():
    out = render_handoff(
        spawned_id=2003,
        title="x",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="bogus",
    )
    # Falls back to task variant: no per-type framing surfaces.
    assert "Reproduce the bug first" not in out
    assert "Findings are the deliverable" not in out
    assert "coordinator" not in out
    # Task end-state guidance present.
    assert "--status verify" in out


def test_render_handoff_bg_variant_omits_tmux_return():
    """E-1568: the bg variant drops every tmux-specific return instruction a
    headless agent cannot execute, and tells it to stop when done."""
    out = render_handoff(
        spawned_id=1568,
        title="Add --bg to spawn",
        return_anchor="%216",
        spawner_task_id=1564,
        task_type="task",
        bg=True,
    )
    # No tmux return lines.
    assert "tmux switch-client" not in out
    assert "tmux move-window" not in out
    # The return-anchor pane id never leaks into bg output.
    assert "%216" not in out
    # bg-specific framing.
    assert "headless background agent" in out
    assert "claude attach" in out
    # Core workflow rules still present.
    assert "E-1568" in out
    assert "STOP and ask" in out
    assert "--status verify" in out
    # The "(with the return line above)" parenthetical is gone for bg.
    assert "return line above" not in out


def test_render_handoff_fg_keeps_tmux_return():
    """Foreground default still carries the tmux return path."""
    out = render_handoff(
        spawned_id=1568,
        title="t",
        return_anchor="%216",
        spawner_task_id=1564,
        task_type="task",
        bg=False,
    )
    assert "tmux switch-client -t %216" in out
    assert "headless background agent" not in out


@pytest.mark.parametrize("ttype", ["task", "bug", "research", "epic"])
def test_render_handoff_bg_variant_all_types(ttype):
    """Every per-type template has a working bg branch."""
    out = render_handoff(
        spawned_id=2500,
        title="x",
        return_anchor="%9",
        spawner_task_id=1000,
        task_type=ttype,
        bg=True,
    )
    assert "tmux switch-client" not in out
    assert "tmux move-window" not in out
    assert "headless background agent" in out


@pytest.mark.parametrize("count", [0, 3])
def test_render_handoff_includes_child_count_when_nonzero(count):
    _seed_project_and_parent(parent_id=2100, child_count=count)
    out = render_handoff(
        spawned_id=2100,
        title="parent task",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="task",
    )
    if count == 0:
        # Zero children → child-count line absent.
        assert "children — read them" not in out
    else:
        # Nonzero → count appears with the --children pointer.
        assert f"This task has {count} children" in out
        assert "endless task show E-2100 --children" in out
