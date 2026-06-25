"""Tests for the children-state breakdown injected into the epic handoff (E-1567).

`_children_state(parent_id)` groups a task's direct children by status,
collapses all terminal statuses into a single `terminal` bucket, renders the
non-empty buckets in lifecycle order, and appends a total. `render_handoff`
computes it unconditionally and passes it as `children_state`; only
`epic.md.tmpl` references the var, so task/bug/research renders consume it as
a no-op. See E-1567.

Like test_handoff.py, the renderer resolves the project root from cwd, so the
test chdirs into a tmp project with a `.endless/` dir.
"""

import pytest

from endless import db
from endless.task_cmd import _children_state, render_handoff


@pytest.fixture(autouse=True)
def chdir_to_handoff_project(tmp_path, monkeypatch):
    """Renderer resolves project root from cwd; give it a `.endless/` dir."""
    (tmp_path / ".endless").mkdir()
    monkeypatch.chdir(tmp_path)


def _seed(parent_id: int, statuses: list[str]) -> None:
    """Insert a project + parent task + one child per status in `statuses`."""
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('cs-test', '/tmp/cs-test', 'active', "
        "datetime('now'), datetime('now'))",
    )
    pid = db.query("SELECT id FROM projects WHERE name = 'cs-test'")[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
        (parent_id, pid, "parent", "underway"),
    )
    for i, status in enumerate(statuses):
        db.execute(
            "INSERT INTO tasks (project_id, parent_id, title, status) "
            "VALUES (?, ?, ?, ?)",
            (pid, parent_id, f"child {i}", status),
        )


# --- _children_state formatting ------------------------------------------


def test_children_state_no_children():
    _seed(3000, [])
    assert _children_state(3000) == "no children yet"


def test_children_state_single_bucket():
    _seed(3001, ["ready", "ready", "ready"])
    assert _children_state(3001) == "3 ready (3 total)"


def test_children_state_mixed_lifecycle_order():
    # Insert out of lifecycle order; output must follow the canonical order.
    _seed(
        3002,
        ["ready", "ready", "ready",
         "unplanned", "unplanned",
         "underway",
         "confirmed", "assumed", "completed", "obsolete"],
    )
    assert (
        _children_state(3002)
        == "2 unplanned, 3 ready, 1 underway, 4 terminal (10 total)"
    )


@pytest.mark.parametrize(
    "terminal_status", ["confirmed", "assumed", "completed", "declined", "obsolete"]
)
def test_children_state_collapses_each_terminal_status(terminal_status):
    """Every terminal status rolls into the single `terminal` bucket."""
    _seed(3003, [terminal_status])
    assert _children_state(3003) == "1 terminal (1 total)"


def test_children_state_terminal_collapse_aggregates_across_statuses():
    """Distinct terminal statuses sum into one `terminal` count."""
    _seed(3004, ["confirmed", "assumed", "completed", "declined", "obsolete"])
    assert _children_state(3004) == "5 terminal (5 total)"


def test_children_state_includes_blocked_and_revisit_buckets():
    """blocked and revisit are in-flight states with their own buckets — no
    child is silently dropped, and the total always reconciles."""
    _seed(
        3005,
        ["unplanned", "blocked", "revisit", "unverified", "confirmed"],
    )
    assert (
        _children_state(3005)
        == "1 unplanned, 1 blocked, 1 revisit, 1 unverified, 1 terminal (5 total)"
    )


# --- epic render: breakdown line + operational-mode block ------------------


def _render_epic(parent_id: int) -> str:
    return render_handoff(
        spawned_id=parent_id,
        title="some epic",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type="epic",
    )


def test_epic_render_shows_no_children_yet():
    _seed(3100, [])
    out = _render_epic(3100)
    assert "Children: no children yet." in out
    # Operational-mode block present.
    assert "Pick your operational mode" in out
    assert "drive decomposition" in out


def test_epic_render_shows_mixed_breakdown():
    _seed(
        3101,
        ["unplanned", "unplanned",
         "ready", "ready", "ready",
         "underway",
         "confirmed", "assumed", "completed", "obsolete"],
    )
    out = _render_epic(3101)
    assert (
        "Children: 2 unplanned, 3 ready, 1 underway, 4 terminal (10 total)."
        in out
    )
    # All six operational modes named.
    for mode in ("Zero children", "All `unplanned`", "All `ready`",
                 "All `underway`", "All terminal", "Mixed"):
        assert mode in out


def test_epic_render_shows_single_bucket():
    _seed(3102, ["ready", "ready", "ready"])
    out = _render_epic(3102)
    assert "Children: 3 ready (3 total)." in out


def test_epic_render_drops_redundant_child_count_block():
    """The old `{{if .child_count}}` block is gone; children_state replaces it."""
    _seed(3103, ["ready", "ready"])
    out = _render_epic(3103)
    assert "This task has" not in out


# --- non-epic renders consume the var as a no-op ---------------------------


@pytest.mark.parametrize("ttype", ["task", "bug", "research"])
def test_non_epic_render_with_children_omits_state_text(ttype):
    """task/bug/research templates don't reference children_state — they render
    cleanly with children present and never surface the breakdown."""
    _seed(3200, ["ready", "unplanned", "confirmed"])
    out = render_handoff(
        spawned_id=3200,
        title="leaf with legacy children",
        return_anchor="%1",
        spawner_task_id=1000,
        task_type=ttype,
    )
    # Renders successfully and names the task.
    assert "E-3200" in out
    # No children-state breakdown leaks into non-epic variants.
    assert "Children:" not in out
    assert "(3 total)" not in out
    assert "Pick your operational mode" not in out
