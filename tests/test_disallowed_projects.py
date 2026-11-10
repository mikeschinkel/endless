"""E-2369: some directories can never be projects — $HOME and above it, OS
system and user-data directories, parent directories — and a project never
nests inside another. The rules live in Go; these check every Python path that
registers honours them."""

import json
import os
from pathlib import Path

from click.testing import CliRunner

from endless import config, db
from endless.cli import main
from endless.project_path import stored

from conftest import TEMP_TREE_REMOVALS, init_git_repo


def _invoke(*args):
    return CliRunner().invoke(main, list(args))


def _register(path, name=None):
    args = ["project", "register", str(path), "--infer"]
    if name:
        args += ["--name", name]
    return _invoke(*args)


def _row(path):
    rows = db.query("SELECT name, status FROM projects WHERE path = ?", (stored(path),))
    return dict(rows[0]) if rows else None


def _home() -> Path:
    return Path(os.environ["HOME"]).resolve()


def _overrides(isolated_env, body: dict, free_temp: bool = True):
    """Write this test's overrides file; free_temp keeps the fixture's removal
    of the temp-tree rules, without which every fixture path is refused."""
    if free_temp:
        body = {**body, "remove": body.get("remove", []) + TEMP_TREE_REMOVALS}
    (isolated_env["config_dir"] / "disallowed-paths.json").write_text(json.dumps(body))
    config.forget_verdicts()


def test_register_refuses_home(isolated_env):
    result = _register(_home(), name="me")
    assert result.exit_code != 0
    assert "cannot be an Endless project" in result.output
    assert _row(_home()) is None


def test_register_refuses_a_directory_an_override_disallows(isolated_env):
    docs = isolated_env["projects_root"] / "Docs"
    docs.mkdir()
    _overrides(isolated_env, {"add": [{"path": str(docs), "scope": "self"}]})

    result = _register(docs)
    assert result.exit_code != 0
    assert "cannot be an Endless project" in result.output

    # SELF scope: a directory inside it is allowed.
    child = init_git_repo(docs / "app")
    result = _register(child)
    assert result.exit_code == 0, result.output


def test_register_refuses_a_project_inside_another(isolated_env):
    outer = init_git_repo(isolated_env["projects_root"] / "outer")
    inner = init_git_repo(outer / "inner")
    assert _register(outer).exit_code == 0

    result = _register(inner)
    assert result.exit_code != 0
    assert "does not nest projects" in result.output
    assert _row(inner) is None


def test_register_refuses_a_directory_holding_a_project(isolated_env):
    inner = init_git_repo(isolated_env["projects_root"] / "group" / "app")
    outer = init_git_repo(inner.parent)
    assert _register(inner).exit_code == 0

    result = _register(outer)
    assert result.exit_code != 0
    assert "does not nest projects" in result.output


def test_unregister_deletes_a_row_at_home(isolated_env):
    db.execute(
        "INSERT INTO projects (name, path, status) VALUES (?, ?, 'active')",
        ("me", "~"),
    )
    listed = _invoke("project", "list")
    assert "cannot be a project" in listed.output

    result = _invoke("project", "unregister", "me")
    assert result.exit_code == 0, result.output
    assert "removed its row" in result.output
    assert _row(_home()) is None


def test_a_home_config_file_does_not_make_home_a_project(isolated_env, monkeypatch):
    from endless.project_path import project_name_for_cwd

    endless_dir = _home() / ".endless"
    endless_dir.mkdir()
    (endless_dir / "config.json").write_text(json.dumps({"name": "me"}))
    assert project_name_for_cwd(_home()) is None


def test_register_refuses_a_repo_in_temp_space(isolated_env):
    # EQ-58: a project is never kept in scratch space. tmp_path is under
    # /private (macOS) or /tmp (Linux), so with the fixture's removal undone
    # the repo is refused.
    repo = init_git_repo(isolated_env["projects_root"] / "scratch")
    _overrides(isolated_env, {}, free_temp=False)

    result = _register(repo)
    assert result.exit_code != 0
    assert "cannot be an Endless project" in result.output
    assert _row(repo) is None


def test_a_config_file_in_a_disallowed_directory_names_no_project(isolated_env):
    # EQ-59: a repo copied into ~/Downloads with its .endless/ must not act as
    # the project it was copied from.
    from endless.project_path import project_name_for_cwd

    copy = _home() / "Downloads" / "endless-main"
    (copy / ".endless").mkdir(parents=True)
    (copy / ".endless" / "config.json").write_text(json.dumps({"name": "endless"}))
    config.forget_verdicts()
    assert project_name_for_cwd(copy) is None


def test_a_config_file_in_a_directory_holding_a_project_names_no_project(isolated_env):
    from endless.project_path import project_name_for_cwd

    inner = init_git_repo(isolated_env["projects_root"] / "group" / "app")
    assert _register(inner).exit_code == 0
    outer = inner.parent
    (outer / ".endless").mkdir()
    (outer / ".endless" / "config.json").write_text(json.dumps({"name": "group"}))
    config.forget_verdicts()
    assert project_name_for_cwd(outer) is None


def test_a_config_file_inside_a_project_still_names_it(isolated_env):
    # A task worktree sits inside its project's main checkout and names the
    # project by its own config file; that must keep working.
    from endless.project_path import project_name_for_cwd

    main_checkout = init_git_repo(isolated_env["projects_root"] / "app")
    assert _register(main_checkout, name="app").exit_code == 0
    wt = main_checkout / ".endless" / "worktrees" / "e-1"
    (wt / ".endless").mkdir(parents=True)
    (wt / ".endless" / "config.json").write_text(json.dumps({"name": "app"}))
    config.forget_verdicts()
    assert project_name_for_cwd(wt) == "app"


def test_a_subdirectory_of_a_project_names_it(isolated_env):
    # The resolver walks up to the nearest registered project; no config file
    # is needed below the project root.
    from endless.project_path import project_name_for_cwd

    app = init_git_repo(isolated_env["projects_root"] / "app")
    assert _register(app, name="app").exit_code == 0
    sub = app / "src" / "pkg"
    sub.mkdir(parents=True)
    config.forget_verdicts()
    assert project_name_for_cwd(sub) == "app"
