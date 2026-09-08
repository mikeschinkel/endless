"""Tests for the generated spawn handoff (E-1469, E-1565, E-1566).

The handoff is rendered from per-type embedded templates under
`templates/handoff/{task,bug,research,epic}.md.tmpl` (E-1566), merged
with the task's id/title and runtime context (worktree, branch).
E-1565 moved rendering from Python string.Template to a
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
        (parent_id, pid, "parent", "underway"),
    )
    for i in range(child_count):
        db.execute(
            "INSERT INTO tasks (project_id, parent_id, title, status) "
            "VALUES (?, ?, ?, ?)",
            (pid, parent_id, f"child {i}", "ready"),
        )


def test_render_handoff_includes_task_and_no_return_line():
    out = render_handoff(
        spawned_id=1469,
        title="Render handoff from template",
        worktree_path="/repo/.endless/worktrees/e-1469",
        branch="task/1469-render-handoff",
        task_type="todo",
    )
    assert "E-1469" in out
    assert "Render handoff from template" in out
    # E-1770: the tmux return line and the spawning-session identity are gone.
    assert "switch-client" not in out
    assert "spawning session's task" not in out
    # Worktree + branch substituted.
    assert "/repo/.endless/worktrees/e-1469" in out
    assert "task/1469-render-handoff" in out
    # Delegates the workflow to the guide and points at the plan.
    assert "endless guide" in out
    assert "endless task show E-1469 --all-fields" in out
    # Generic handoff rules that apply to every spawn.
    assert "STOP and ask" in out
    assert "Don't mark `confirmed`/`assumed`" in out


def test_render_handoff_root_label_has_no_parent_prefix():
    """E-1620: a root task's opening identity line is `E-<id>: <title>`."""
    out = render_handoff(
        spawned_id=1620,
        title="Render hierarchical labels",
        task_type="todo",
    )
    assert "- E-1620: Render hierarchical labels." in out


def test_render_handoff_child_label_includes_parent_prefix():
    """E-1620: a parented task's opening line is `E-<parent>/E-<id>: <title>`."""
    out = render_handoff(
        spawned_id=1620,
        title="Render hierarchical labels",
        task_type="bugfix",
        parent_id=1564,
    )
    assert "- E-1564/E-1620: Render hierarchical labels." in out
    # The bare-id references elsewhere in the handoff stay unprefixed.
    assert "endless task show E-1620 --all-fields" in out


def test_render_handoff_degrades_without_runtime_context():
    out = render_handoff(
        spawned_id=1469,
        title="t",
        task_type=None,
    )
    # Still renders; missing context becomes visible placeholders rather
    # than crashing or leaving a blank.
    assert "E-1469" in out
    assert "<task worktree>" in out
    assert "<task branch>" in out


def test_render_handoff_bug_variant():
    out = render_handoff(
        spawned_id=2000,
        title="Crash on empty input",
        task_type="bugfix",
    )
    # Bug-specific framing.
    assert "Reproduce the bug first" in out
    # Bug still goes to unverified like a task.
    assert "--status unverified" in out


def test_render_handoff_research_variant():
    out = render_handoff(
        spawned_id=2001,
        title="Survey caching strategies",
        task_type="research",
    )
    # Research-specific framing.
    assert "Findings are the deliverable" in out
    # End state guidance points at the review gate + outcome (file form,
    # E-1001). E-2016 moved this off `completed`: research reports done at
    # `unreviewed` and leaves the terminal to the user, who has to read the
    # findings first.
    assert "--status unreviewed --outcome-file" in out
    assert "--status completed" not in out
    # Research must NOT instruct --status unverified (its own gate per ED-1502).
    assert "--status unverified" not in out


def test_render_handoff_epic_variant():
    out = render_handoff(
        spawned_id=2002,
        title="Migrate ingestion pipeline",
        task_type="epic",
    )
    # Epic-specific framing.
    assert "coordinator" in out
    assert "draft plans" in out
    # Step 3 names the children as the units of work; the --all-fields read in
    # step 2 already listed them, so it points at that output, not at a second
    # narrower command.
    assert "Read the children in that output" in out
    assert "--children" not in out
    # Step 6 points at epic completion.
    assert "--status completed" in out
    # Epics never go to unverified (children do their own verification).
    assert "--status unverified" not in out


def test_render_handoff_unknown_type_falls_back_to_task():
    out = render_handoff(
        spawned_id=2003,
        title="x",
        task_type="bogus",
    )
    # Falls back to task variant: no per-type framing surfaces.
    assert "Reproduce the bug first" not in out
    assert "Findings are the deliverable" not in out
    assert "coordinator" not in out
    # Task end-state guidance present.
    assert "--status unverified" in out


def test_render_handoff_omits_tmux_return():
    """E-1770: the handoff carries no tmux return line.

    This replaces THREE tests. Two of them (bg_variant_omits_tmux_return,
    bg_variant_all_types) pinned the `bg=True` variant E-1568 added for headless
    agents; E-2074 removed background agents and the `bg` var with them, so
    there is one variant left to check.
    """
    out = render_handoff(
        spawned_id=1568,
        title="t",
        task_type="todo",
    )
    assert "tmux switch-client" not in out
    assert "tmux move-window" not in out
    assert "spawning session's task" not in out
    # E-2074: no handoff may mention background agents any more.
    assert "headless background agent" not in out
    assert "claude attach" not in out
    # Core workflow rules still present.
    assert "E-1568" in out
    assert "STOP and ask" in out
    assert "--status unverified" in out


@pytest.mark.parametrize("ttype", ["task", "bug", "research", "epic"])
def test_render_handoff_all_types_omit_tmux_return(ttype):
    """Every per-type template renders, and none carries a tmux return line."""
    out = render_handoff(
        spawned_id=2500,
        title="x",
        task_type=ttype,
    )
    assert "tmux switch-client" not in out
    assert "tmux move-window" not in out
    assert "headless background agent" not in out


@pytest.mark.parametrize("count", [0, 3])
def test_render_handoff_includes_child_count_when_nonzero(count):
    _seed_project_and_parent(parent_id=2100, child_count=count)
    out = render_handoff(
        spawned_id=2100,
        title="parent task",
        task_type="todo",
    )
    if count == 0:
        # Zero children → child-count line absent.
        assert "This task has" not in out
    else:
        # Nonzero → the count is the signal. The line no longer offers a
        # `--children` command: `--all-fields` in step 2 already rendered them.
        assert f"This task has {count} children" in out
        assert "the `--all-fields` read includes them" in out
        assert "--children" not in out
