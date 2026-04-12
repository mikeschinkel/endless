"""Tests for register and related commands."""

import json
from pathlib import Path

from endless import db, config
from endless.register import register_project, validate_name


def test_validate_name_accepts_valid():
    assert validate_name("my-project")
    assert validate_name("go-tealeaves")
    assert validate_name("h2pp")
    assert validate_name("project_1")


def test_validate_name_rejects_invalid():
    assert not validate_name("My Project")
    assert not validate_name("UPPER")
    assert not validate_name("-starts-with-dash")
    assert not validate_name("")
    assert not validate_name("has spaces")


def test_register_creates_config_and_db_entry(isolated_env):
    project_dir = isolated_env["projects_root"] / "new-proj"
    project_dir.mkdir()

    name = register_project(project_dir, infer=True)
    assert name == "new-proj"

    # Check DB
    rows = db.query(
        "SELECT name, path, status FROM projects WHERE name=?",
        ("new-proj",),
    )
    assert len(rows) == 1
    assert rows[0]["status"] == "active"

    # Check config on disk
    cfg = config.project_config_read(project_dir)
    assert cfg["name"] == "new-proj"


def test_register_with_explicit_fields(isolated_env):
    project_dir = isolated_env["projects_root"] / "custom"
    project_dir.mkdir()

    register_project(
        project_dir,
        name="custom",
        label="Custom Project",
        description="A custom one",
        language="python",
        status="idea",
        infer=True,
    )

    row = db.query(
        "SELECT label, description, language, status "
        "FROM projects WHERE name=?",
        ("custom",),
    )[0]
    assert row["label"] == "Custom Project"
    assert row["description"] == "A custom one"
    assert row["language"] == "python"
    assert row["status"] == "idea"


def test_register_update_existing(isolated_env):
    project_dir = isolated_env["projects_root"] / "updatable"
    project_dir.mkdir()

    register_project(project_dir, infer=True)
    register_project(
        project_dir, label="Updated Label", infer=True,
    )

    rows = db.query(
        "SELECT label FROM projects WHERE name=?",
        ("updatable",),
    )
    assert len(rows) == 1
    assert rows[0]["label"] == "Updated Label"


def test_register_detects_group_name(isolated_env):
    group_dir = isolated_env["projects_root"] / "my-group"
    group_dir.mkdir()
    project_dir = group_dir / "sub-proj"
    project_dir.mkdir()

    register_project(project_dir, infer=True)

    row = db.query(
        "SELECT group_name FROM projects WHERE name=?",
        ("sub-proj",),
    )[0]
    assert row["group_name"] == "my-group"
