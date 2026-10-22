"""E-2349: `endless project forget` drops a mistaken registration without
writing to the project's files, and `worktree land` stops counting Endless's
runtime files as user work."""

import json
import subprocess

from click.testing import CliRunner

from endless import db
from endless.cli import main
from endless.project_path import stored
from endless.worktree_cmd import _git_status_partition

from conftest import init_git_repo


def _invoke(*args):
    return CliRunner().invoke(main, list(args))


def _register(path):
    result = _invoke("project", "register", str(path), "--infer")
    assert result.exit_code == 0, result.output


def _row(row_id):
    return dict(db.query(
        "SELECT name, path, status, slug, identity, forgotten_from "
        "FROM projects WHERE id = ?", (row_id,),
    )[0])


def test_forget_tombstones_the_row_and_writes_nothing_to_the_project(isolated_env):
    proj = init_git_repo(isolated_env["projects_root"] / "e-2206")
    _register(proj)
    row_id = db.query("SELECT id FROM projects WHERE name = 'e-2206'")[0]["id"]
    config_before = (proj / ".endless" / "config.json").read_bytes()

    result = _invoke("project", "forget", "e-2206")
    assert result.exit_code == 0, result.output
    assert "Forgot e-2206" in result.output

    row = _row(row_id)
    assert row["status"] == "forgotten"
    assert row["name"] == f"forgotten-{row_id}"
    assert row["slug"] is None and row["identity"] is None
    held = json.loads(row["forgotten_from"])
    assert held["name"] == "e-2206" and held["path"] == stored(proj)
    assert (proj / ".endless" / "config.json").read_bytes() == config_before
    assert not (proj / ".endless-ignore").exists()
    assert "e-2206" not in _invoke("project", "list").output

    # The same checkout registers again, as a new project.
    _register(proj)
    assert db.query(
        "SELECT status FROM projects WHERE path = ?", (stored(proj),)
    )[0]["status"] == "active"


def test_forget_accepts_a_path_and_an_ignored_row(isolated_env):
    proj = init_git_repo(isolated_env["projects_root"] / "mistake")
    _register(proj)
    assert _invoke("project", "unregister", "mistake").exit_code == 0

    result = _invoke("project", "forget", str(proj))
    assert result.exit_code == 0, result.output
    assert not db.query(
        "SELECT 1 FROM projects WHERE path = ?", (stored(proj),))


def test_forget_survives_a_project_list_sync(isolated_env):
    """The sync deletes rows whose path is gone; a tombstone's never exists."""
    proj = init_git_repo(isolated_env["projects_root"] / "gone")
    _register(proj)
    row_id = db.query("SELECT id FROM projects WHERE name = 'gone'")[0]["id"]
    assert _invoke("project", "forget", "gone").exit_code == 0

    _invoke("project", "list")
    assert _row(row_id)["status"] == "forgotten"


def _add_task(title):
    result = _invoke("task", "add", title, "--description", "d")
    assert result.exit_code == 0, result.output
    return db.query("SELECT id FROM tasks WHERE title = ?", (title,))[0]["id"]


def test_forget_refuses_a_project_with_tasks(seeded_project_at_cwd):
    _add_task("Fix this thing")
    result = _invoke("project", "forget", "test")
    assert result.exit_code != 0
    assert "--with-tasks" in result.output and "project ignore" in result.output
    assert db.query("SELECT status FROM projects WHERE name = 'test'")


def test_forget_with_tasks_removes_them_then_forgets(seeded_project_at_cwd):
    ids = [_add_task("Fix this one"), _add_task("Fix that one")]
    result = _invoke("project", "forget", "test", "--with-tasks")
    assert result.exit_code == 0, result.output
    assert "Removed 2 task(s)" in result.output
    for task_id in ids:
        assert db.query(
            "SELECT removed FROM tasks WHERE id = ?", (task_id,)
        )[0]["removed"] == 1
    assert not db.query("SELECT 1 FROM projects WHERE name = 'test'")


def test_forget_with_tasks_refuses_while_a_relation_exists(seeded_project_at_cwd):
    a, b = _add_task("Fix the source"), _add_task("Fix the target")
    linked = _invoke("task", "link", str(a), "--to", f"E-{b}",
                     "--type", "relates_to")
    assert linked.exit_code == 0, linked.output

    result = _invoke("project", "forget", "test", "--with-tasks")
    assert result.exit_code != 0
    assert "relation" in result.output
    assert db.query("SELECT removed FROM tasks WHERE id = ?", (a,))[0]["removed"] == 0
    assert db.query("SELECT 1 FROM projects WHERE name = 'test'")


# --- land ----------------------------------------------------------------------

def test_land_partition_skips_endless_runtime_paths(tmp_path):
    repo = init_git_repo(tmp_path / "repo")
    (repo / ".endless").mkdir()
    (repo / ".endless" / "config.json").write_text("{}\n")
    for args in (["add", ".endless/config.json"],
                 ["-c", "user.email=t@e.com", "-c", "user.name=T",
                  "commit", "-q", "-m", "config"]):
        subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True)
    (repo / ".endless" / "logs").mkdir(parents=True)
    (repo / ".endless" / "logs" / "session-notices.jsonl").write_text("{}\n")
    (repo / ".endless" / "worktree.json").write_text("{}\n")
    (repo / ".endless" / "worktrees" / "e-1").mkdir(parents=True)
    (repo / "notes.txt").write_text("mine\n")

    auto, user = _git_status_partition(repo)
    assert auto == []
    assert user == ["notes.txt"]
    # Without a .gitignore entry git still sees them; land just doesn't count them.
    out = subprocess.run(["git", "status", "--porcelain"], cwd=repo,
                         capture_output=True, text=True).stdout
    assert ".endless/" in out
