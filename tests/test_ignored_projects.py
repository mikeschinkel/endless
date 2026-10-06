"""E-2251: "not a project" lives on projects rows (status 'ignored') plus an
optional .endless-ignore marker, and every registration path honours it."""

import json

from click.testing import CliRunner

from endless import config, db
from endless.cli import main
from endless.project_path import stored
from endless.reconcile import reconcile


def _row(path):
    rows = db.query(
        "SELECT name, status FROM projects WHERE path = ?", (stored(path),)
    )
    return dict(rows[0]) if rows else None


def _invoke(*args):
    result = CliRunner().invoke(main, list(args))
    return result


def _register(path, name=None):
    args = ["project", "register", str(path), "--infer"]
    if name:
        args += ["--name", name]
    result = _invoke(*args)
    assert result.exit_code == 0, result.output
    return result


def test_unregister_keeps_the_row_as_ignored_and_register_restores_it(isolated_env):
    project_dir = isolated_env["projects_root"] / "keeper"
    project_dir.mkdir()
    _register(project_dir)

    result = _invoke("project", "unregister", "keeper")
    assert result.exit_code == 0, result.output
    assert _row(project_dir) == {"name": "keeper", "status": "ignored"}
    assert "keeper" not in _invoke("project", "list").output
    assert config.is_ignored(project_dir)

    _register(project_dir)
    assert _row(project_dir) == {"name": "keeper", "status": "active"}


def test_ignore_lists_and_unignore_clears(isolated_env):
    target = isolated_env["projects_root"] / "not-mine"
    target.mkdir()

    result = _invoke("project", "ignore", str(target))
    assert result.exit_code == 0, result.output
    assert _row(target)["status"] == "ignored"
    listed = _invoke("project", "ignore")
    assert stored(target) in listed.output

    result = _invoke("project", "unignore", str(target))
    assert result.exit_code == 0, result.output
    assert _row(target) is None
    assert not config.is_ignored(target)


def test_marker_option_writes_marker_and_git_exclude(isolated_env):
    import subprocess

    repo = isolated_env["projects_root"] / "vendored"
    repo.mkdir()
    subprocess.run(["git", "init", "-q", str(repo)], check=True)

    result = _invoke("project", "ignore", str(repo), "--marker")
    assert result.exit_code == 0, result.output
    assert (repo / config.IGNORE_MARKER).is_file()
    exclude = (repo / ".git" / "info" / "exclude").read_text().splitlines()
    assert "/" + config.IGNORE_MARKER in exclude

    status = subprocess.run(
        ["git", "-C", str(repo), "status", "--porcelain"],
        capture_output=True, text=True, check=True,
    ).stdout
    assert config.IGNORE_MARKER not in status


def test_register_refuses_a_directory_carrying_the_marker(isolated_env):
    target = isolated_env["projects_root"] / "hands-off"
    target.mkdir()
    (target / config.IGNORE_MARKER).write_text("")

    result = _invoke("project", "register", str(target), "--infer")
    assert result.exit_code != 0
    assert config.IGNORE_MARKER in result.output
    assert _row(target) is None


def test_explicit_register_is_allowed_under_an_ignored_parent(isolated_env):
    """Rule A: ignore ~/Projects, still register ~/Projects/endless."""
    parent = isolated_env["projects_root"] / "tree"
    child = parent / "mine"
    child.mkdir(parents=True)
    config.add_ignore(parent)

    _register(child)
    assert _row(child)["status"] == "active"
    assert not config.is_ignored(child / "src")
    assert config.is_ignored(parent / "other")


def test_reconcile_skips_projects_under_an_ignored_directory(isolated_env):
    parent = isolated_env["projects_root"] / "third-party"
    child = parent / "lib"
    (child / ".endless").mkdir(parents=True)
    (child / ".endless" / "config.json").write_text(
        json.dumps({"name": "lib", "status": "active", "type": "project"})
    )
    config.mark_as_group(parent)
    config.add_ignore(parent)

    reconcile()
    assert _row(child) is None


def test_reconcile_leaves_ignored_rows_alone(isolated_env):
    project_dir = isolated_env["projects_root"] / "gone-quiet"
    project_dir.mkdir()
    _register(project_dir)
    assert _invoke("project", "unregister", "gone-quiet").exit_code == 0
    # An ignored path that does not exist yet still records intent.
    ghost = isolated_env["projects_root"] / "not-yet"
    ghost.mkdir()
    config.add_ignore(ghost)
    ghost.rmdir()

    reconcile()
    assert _row(project_dir)["status"] == "ignored"
    assert _row(ghost)["status"] == "ignored"
