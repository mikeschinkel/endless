"""Shared test fixtures for Endless tests."""

import json
import os
import sqlite3
from pathlib import Path

import pytest

from endless import config, db


@pytest.fixture(autouse=True)
def isolated_env(tmp_path, monkeypatch):
    """Isolate every test from the real config/DB/filesystem.

    Sets up:
    - tmp config dir (overrides CONFIG_DIR, DB_PATH, CONFIG_FILE)
    - XDG_CONFIG_HOME env var pointing at tmp (so Go subprocesses are isolated)
    - tmp projects root with a sample project
    - fresh DB with schema applied
    """
    config_dir = tmp_path / ".config" / "endless"
    config_dir.mkdir(parents=True)

    projects_root = tmp_path / "Projects"
    projects_root.mkdir()

    # Override config module paths
    monkeypatch.setattr(config, "CONFIG_DIR", config_dir)
    monkeypatch.setattr(config, "CONFIG_FILE", config_dir / "config.json")
    monkeypatch.setattr(config, "DB_PATH", config_dir / "endless.db")
    monkeypatch.setattr(db, "DB_PATH", config_dir / "endless.db")

    # Set XDG_CONFIG_HOME so Go subprocesses (e.g. endless-event invoked by
    # event_bridge.emit_event) resolve to the same isolated DB as the Python
    # in-process code. monkeypatch.setattr only affects the current process.
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / ".config"))

    # Always auto-migrate in tests, regardless of the developer's shell setting
    # for ENDLESS_AUTO_MIGRATE. Tests need a fully migrated schema.
    monkeypatch.setenv("ENDLESS_AUTO_MIGRATE", "1")

    # Prepend this worktree's bin/ to PATH so subprocesses (e.g. endless-event
    # invoked by event_bridge.emit_event) find the locally-built binary, not
    # the globally-installed one symlinked from a sibling worktree.
    bin_dir = Path(__file__).resolve().parent.parent / "bin"
    monkeypatch.setenv("PATH", f"{bin_dir}:{os.environ.get('PATH', '')}")

    # Reset DB connection so it creates a fresh one
    monkeypatch.setattr(db, "_conn", None)

    # Write default config pointing to tmp projects root
    cfg = {
        "roots": [str(projects_root)],
        "scan_interval": 300,
        "ignore": [],
    }
    with open(config_dir / "config.json", "w") as f:
        json.dump(cfg, f)

    # Initialize DB
    db.get_db()

    yield {
        "config_dir": config_dir,
        "projects_root": projects_root,
        "db_path": config_dir / "endless.db",
    }


@pytest.fixture
def seeded_project_at_cwd(isolated_env, monkeypatch):
    """Chdir into a clean tmp project dir and register a project there.

    Tests that exercise functions emitting events (which call _resolve_project(None))
    need cwd to resolve to a registered project. The default cwd is the endless repo,
    whose .endless/config.json gives a name that won't be in the test DB. This
    fixture chdir's to a clean tmp dir and seeds the project record at that path.
    """
    proj_dir = isolated_env["projects_root"]
    monkeypatch.chdir(proj_dir)
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
    )
    return proj_dir


@pytest.fixture
def sample_project(isolated_env):
    """Create a sample project directory with .endless/config.json."""
    project_dir = isolated_env["projects_root"] / "my-project"
    project_dir.mkdir()
    endless_dir = project_dir / ".endless"
    endless_dir.mkdir()

    cfg = {
        "name": "my-project",
        "label": "My Project",
        "description": "A test project",
        "language": "go",
        "status": "active",
        "dependencies": [],
        "documents": {"rules": []},
    }
    with open(endless_dir / "config.json", "w") as f:
        json.dump(cfg, f)

    return project_dir


@pytest.fixture
def registered_project(sample_project):
    """A sample project that's also registered in the DB."""
    from endless.register import register_project
    register_project(sample_project, infer=True)
    return sample_project
