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

from conftest import init_git_repo


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


def _overrides(isolated_env, body: dict):
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
