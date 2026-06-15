"""Tests for the generated spawn handoff (E-1469, E-1565).

The handoff is rendered from the embedded `handoff.md.tmpl` template, merged
with the task's id/title and runtime context (spawning pane, spawning
session's task). E-1565 moved rendering from Python string.Template to a
shell-out to `endless-go template render`, so the test chdirs into a tmp
project (one with a `.endless/` subdir) so the renderer can resolve a
project context.
"""

import pytest

from endless.task_cmd import render_handoff


@pytest.fixture(autouse=True)
def chdir_to_handoff_project(tmp_path, monkeypatch):
    """Renderer resolves project root from cwd; give it a `.endless/` dir."""
    (tmp_path / ".endless").mkdir()
    monkeypatch.chdir(tmp_path)


def test_render_handoff_includes_task_and_return_path():
    out = render_handoff(
        spawned_id=1469,
        title="Render handoff from template",
        return_anchor="%7",
        spawner_task_id=1400,
        worktree_path="/repo/.endless/worktrees/e-1469",
        branch="task/1469-render-handoff",
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
    )
    # Still renders; missing context becomes visible placeholders rather
    # than crashing or leaving a blank.
    assert "E-1469" in out
    assert "E-?" in out
    assert "%<spawning-pane>" in out
    assert "<task worktree>" in out
    assert "<task branch>" in out
