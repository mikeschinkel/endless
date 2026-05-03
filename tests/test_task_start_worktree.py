"""Tests for E-971 Layer F: end-to-end worktree creation on `task start`.

Exercises start_item against a real git project + DB.
"""

import json
import subprocess
from pathlib import Path

import click
import pytest

from endless import db


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def project_with_task(seeded_project_at_cwd):
    """tmp dir with: git repo, registered project at cwd, one task inserted.

    Returns dict with: project_root, task_id, title.
    """
    repo = seeded_project_at_cwd
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)

    proj_id = db.query("SELECT id FROM projects WHERE path = ?", (str(repo),))[0]["id"]
    title = "Move title verbs from hardcoded list to database table"
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, title, title),
    )
    task_id = db.query("SELECT id FROM tasks WHERE title = ?", (title,))[0]["id"]
    return {"project_root": repo, "task_id": task_id, "title": title}


def test_start_creates_worktree_no_plan_file(project_with_task, capsys):
    from endless.task_cmd import start_item

    start_item(project_with_task["task_id"])

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert wt.exists()
    assert wt.is_dir()

    companion = json.loads((wt / ".endless" / "worktree.json").read_text())
    assert companion["kind"] == "task"
    assert companion["task_id"] == f"E-{tid}"
    assert companion["base_branch"] == "main"
    assert companion["branch"].startswith(f"task/{tid}-")
    assert "created_at" in companion

    # Slug should derive from title
    assert companion["branch"] == f"task/{tid}-move-title-verbs-hardcoded-list-database"

    # Branch exists in git
    branches = subprocess.run(
        ["git", "branch"], cwd=repo, capture_output=True, text=True, check=True,
    ).stdout
    assert f"task/{tid}-move-title-verbs-hardcoded-list-database" in branches

    # User-facing output
    captured = capsys.readouterr()
    assert "Created task worktree:" in captured.out
    assert "Switch sessions to it: cd" in captured.out
    assert f"endless task spawn E-{tid}" in captured.out


def test_start_idempotent_on_second_run(project_with_task, capsys):
    from endless.task_cmd import start_item

    start_item(project_with_task["task_id"])
    capsys.readouterr()  # clear

    start_item(project_with_task["task_id"])
    captured = capsys.readouterr()
    assert "Worktree already exists:" in captured.out

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    branches = subprocess.run(
        ["git", "branch"], cwd=repo, capture_output=True, text=True, check=True,
    ).stdout
    # Only one task branch
    assert branches.count(f"task/{tid}-") == 1


def test_start_refuses_when_plan_file_uncommitted(project_with_task, capsys):
    from endless.task_cmd import start_item

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    plans = repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / f"E-{tid}.md").write_text("plan content\n")

    with pytest.raises(click.ClickException) as exc_info:
        start_item(tid)

    msg = exc_info.value.message
    assert f".endless/plans/E-{tid}.md" in msg
    assert "git -C" in msg
    assert f"endless task start E-{tid}" in msg

    # No worktree created
    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert not wt.exists()


def test_start_succeeds_when_plan_file_committed(project_with_task):
    from endless.task_cmd import start_item

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    plans = repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / f"E-{tid}.md").write_text("plan content\n")
    _run(["git", "add", f".endless/plans/E-{tid}.md"], repo)
    _run(["git", "commit", "-q", "-m", "add plan"], repo)

    start_item(tid)

    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert wt.exists()
    # Plan file rides into the worktree
    assert (wt / ".endless" / "plans" / f"E-{tid}.md").exists()


def test_start_uses_task_fallback_for_all_filler_title(seeded_project_at_cwd):
    from endless.task_cmd import start_item

    repo = seeded_project_at_cwd
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)

    proj_id = db.query("SELECT id FROM projects WHERE path = ?", (str(repo),))[0]["id"]
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, "The to from", "filler"),
    )
    tid = db.query("SELECT id FROM tasks WHERE title = ?", ("The to from",))[0]["id"]

    start_item(tid)

    companion = json.loads(
        (repo / ".endless" / "worktrees" / f"e-{tid}" / ".endless" / "worktree.json").read_text()
    )
    assert companion["branch"] == f"task/{tid}-task"


def test_start_worktree_discoverable_via_for_task(project_with_task):
    """After start, the new worktree shows up in worktree list / for-task."""
    from endless.task_cmd import start_item
    from endless.worktree_cmd import _branch_for_task, _enriched_list

    start_item(project_with_task["task_id"])

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    rows = _enriched_list(repo)
    match = _branch_for_task(rows, f"E-{tid}")
    assert match is not None
    assert match["state"] == "active"
    assert Path(match["path"]) == repo / ".endless" / "worktrees" / f"e-{tid}"
