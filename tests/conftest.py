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
