"""Tests for config module."""

import json
from pathlib import Path

from endless import config


def test_load_config_returns_defaults(isolated_env):
    cfg = config.load_config()
    assert "roots" in cfg
    assert "ignore" in cfg
    assert cfg["scan_interval"] == 300


def test_save_and_load_roundtrip(isolated_env):
    cfg = config.load_config()
    cfg["scan_interval"] = 600
    config.save_config(cfg)

    reloaded = config.load_config()
    assert reloaded["scan_interval"] == 600


def test_get_roots_expands_paths(isolated_env):
    roots = config.get_roots()
    assert len(roots) == 1
    assert roots[0].is_dir()


def test_add_ignore_and_check(isolated_env):
    path = isolated_env["projects_root"] / "some-dir"
    assert not config.is_ignored(path)

    config.add_ignore(path)
    assert config.is_ignored(path)


def test_ignore_is_idempotent(isolated_env):
    path = isolated_env["projects_root"] / "some-dir"
    config.add_ignore(path)
    config.add_ignore(path)

    cfg = config.load_config()
    # Should only appear once
    short = str(path).replace(str(Path.home()), "~")
    assert cfg["ignore"].count(short) <= 1


def test_child_of_ignored_is_ignored(isolated_env):
    parent = isolated_env["projects_root"] / "parent-dir"
    child = parent / "child-dir"

    config.add_ignore(parent)
    assert config.is_ignored(child)


def test_project_config_write_and_read(isolated_env):
    project_dir = isolated_env["projects_root"] / "test-proj"
    project_dir.mkdir()

    data = {"name": "test-proj", "label": "Test", "status": "active"}
    config.project_config_write(project_dir, data)

    result = config.project_config_read(project_dir)
    assert result["name"] == "test-proj"
    assert result["label"] == "Test"


def test_mark_as_group(isolated_env):
    group_dir = isolated_env["projects_root"] / "my-group"
    group_dir.mkdir()

    assert not config.is_group_dir(group_dir)

    config.mark_as_group(group_dir)
    assert config.is_group_dir(group_dir)

    cfg = config.project_config_read(group_dir)
    assert cfg["type"] == "group"
    assert cfg["name"] == "my-group"
