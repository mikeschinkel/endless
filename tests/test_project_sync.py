"""Tests for the project sync — filesystem as source of truth."""

import json
import shutil
from pathlib import Path

from endless import db, config
from endless.register import register_project
from endless.project_sync import sync_projects


def test_sync_projects_retires_a_project_whose_directory_is_gone(registered_project):
    """The sweep marks the row ignored and keeps it (ED-1547, E-2010).

    It used to DELETE, and eight of the ten references to projects are
    ON DELETE CASCADE, so an automatic sweep silently took the project's whole
    history with it.
    """
    row = db.query("SELECT id FROM projects WHERE name='my-project'")
    assert len(row) == 1
    project_id = row[0]["id"]

    shutil.rmtree(registered_project)

    sync_projects()

    # The row survives, under its original id.
    kept = db.query("SELECT id, status FROM projects WHERE name='my-project'")
    assert len(kept) == 1, "the sweep deleted the row instead of retiring it"
    assert kept[0]["id"] == project_id, "the id must not change"
    assert kept[0]["status"] == config.IGNORED_STATUS

    # And it is no longer a project any reader lists.
    assert not db.exists(
        "SELECT 1 FROM live_projects WHERE name='my-project'"
    )


def test_sync_projects_keeps_the_history_of_a_vanished_project(registered_project):
    """The cascade that used to fire. This is the measured loss, in miniature.

    A task and an activity row recorded against the project must still be there
    after the sweep, and still point at the same project id — the thing that
    made the original data loss invisible was that the rows were simply gone,
    with nothing left to notice.
    """
    project_id = db.query(
        "SELECT id FROM projects WHERE name='my-project'"
    )[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase, type_id, updated_at) "
        "VALUES (9401, ?, 'survivor', 'ready', 'now', 1, '2026-11-07T10:00:00')",
        (project_id,),
    )
    db.execute(
        "INSERT INTO activity (project_id, source, created_at) "
        "VALUES (?, 'e-2010-fixture', '2026-11-07T10:00:00')",
        (project_id,),
    )

    shutil.rmtree(registered_project)

    sync_projects()

    task = db.query("SELECT project_id FROM tasks WHERE id=9401")
    assert len(task) == 1, "the sweep cascaded the project's tasks away"
    assert task[0]["project_id"] == project_id
    assert db.exists(
        "SELECT 1 FROM activity WHERE project_id=? AND source='e-2010-fixture'",
        (project_id,),
    ), "the sweep cascaded the project's activity away"


def test_sync_projects_revives_a_restored_project_on_re_register(
    registered_project, isolated_env
):
    """`project register` re-activates THAT row, id and history intact.

    This is the half that makes retirement acceptable: the sweep deliberately
    does not auto-revive, so the path back has to be real.

    The directory is moved aside and restored rather than recreated, because
    that is the case this models — an unmounted volume, a restored backup. A
    fresh `git init` would be a different project: Endless identifies one by
    its repo's root commit, so recreating the directory would correctly mint a
    new row and would prove nothing about revival.
    """
    project_path = Path(registered_project)
    project_id = db.query(
        "SELECT id FROM projects WHERE name='my-project'"
    )[0]["id"]

    # Outside the scanned roots, deliberately. Moved to a sibling INSIDE the
    # root the sweep would see it at its new path and treat it as a move, which
    # is a different (and already tested) branch.
    stash = isolated_env["projects_root"].parent / "e-2010-stashed-project"
    shutil.move(str(project_path), str(stash))

    sync_projects()
    assert not db.exists("SELECT 1 FROM live_projects WHERE name='my-project'"), (
        "the sweep left a project listed whose directory is gone"
    )

    shutil.move(str(stash), str(project_path))
    # infer=True for the same reason the registered_project fixture passes it:
    # register prompts for the name otherwise, and a prompt in a test hangs.
    register_project(project_path, infer=True)

    # By id, not by path: `projects.path` is stored in a normalized, possibly
    # home-relative spelling, so comparing it against the string this test
    # happens to hold would test the spelling rather than the revival.
    revived = db.query("SELECT id FROM live_projects WHERE id=?", (project_id,))
    assert len(revived) == 1, (
        "re-registering did not bring back THAT row — a new id orphans every "
        "task, session and activity row recorded against the old one"
    )


def test_sync_projects_updates_moved_project(registered_project, isolated_env):
    new_path = isolated_env["projects_root"] / "moved-project"
    shutil.move(str(registered_project), str(new_path))

    sync_projects()

    row = db.query(
        "SELECT path FROM projects WHERE name='my-project'"
    )
    assert len(row) == 1
    assert row[0]["path"] == str(new_path)


def test_sync_projects_picks_up_new_project_on_disk(isolated_env):
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

    sync_projects()

    assert db.exists(
        "SELECT 1 FROM projects WHERE name='found-on-disk'"
    )
    row = db.query(
        "SELECT label, language FROM projects "
        "WHERE name='found-on-disk'"
    )[0]
    assert row["label"] == "Found"
    assert row["language"] == "go"


def test_sync_projects_syncs_config_changes(registered_project):
    # Change config on disk
    cfg = config.project_config_read(registered_project)
    cfg["label"] = "Changed Label"
    cfg["status"] = "paused"
    config.project_config_write(registered_project, cfg)

    sync_projects()

    row = db.query(
        "SELECT label, status FROM projects "
        "WHERE name='my-project'"
    )[0]
    assert row["label"] == "Changed Label"
    assert row["status"] == "paused"
