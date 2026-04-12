"""Tests for reconciliation — filesystem as source of truth."""

import json
import shutil
from pathlib import Path

from endless import db, config
from endless.register import register_project
from endless.reconcile import reconcile


def test_reconcile_removes_deleted_project(registered_project):
    # Verify it exists
    assert db.exists(
        "SELECT 1 FROM projects WHERE name='my-project'"
    )

    # Delete the project directory
    shutil.rmtree(registered_project)

    reconcile()

    # Should be gone from DB
    assert not db.exists(
        "SELECT 1 FROM projects WHERE name='my-project'"
    )


def test_reconcile_updates_moved_project(registered_project, isolated_env):
    new_path = isolated_env["projects_root"] / "moved-project"
    shutil.move(str(registered_project), str(new_path))

    reconcile()

    row = db.query(
        "SELECT path FROM projects WHERE name='my-project'"
    )
    assert len(row) == 1
    assert row[0]["path"] == str(new_path)


def test_reconcile_picks_up_new_project_on_disk(isolated_env):
    # Create a project on disk without using register
    project_dir = isolated_env["projects_root"] / "found-on-disk"
    project_dir.mkdir()
    endless_dir = project_dir / ".endless"
    endless_dir.mkdir()
    cfg = {
        "name": "found-on-disk",
        "label": "Found",
        "description": "",
        "language": "go",
        "status": "active",
    }
    with open(endless_dir / "config.json", "w") as f:
        json.dump(cfg, f)

    assert not db.exists(
        "SELECT 1 FROM projects WHERE name='found-on-disk'"
    )

    reconcile()

    assert db.exists(
        "SELECT 1 FROM projects WHERE name='found-on-disk'"
    )
    row = db.query(
        "SELECT label, language FROM projects "
        "WHERE name='found-on-disk'"
    )[0]
    assert row["label"] == "Found"
    assert row["language"] == "go"


def test_reconcile_syncs_config_changes(registered_project):
    # Change config on disk
    cfg = config.project_config_read(registered_project)
    cfg["label"] = "Changed Label"
    cfg["status"] = "paused"
    config.project_config_write(registered_project, cfg)

    reconcile()

    row = db.query(
        "SELECT label, status FROM projects "
        "WHERE name='my-project'"
    )[0]
    assert row["label"] == "Changed Label"
    assert row["status"] == "paused"
