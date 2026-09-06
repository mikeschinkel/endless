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


# ═══ E-1934 — durable-content gate settings ════════════════════════════════

import json as _json

import pytest as _pytest

from endless.config import CITATION_EXTENSIONS, project_content_config


def _project(tmp_path, content):
    (tmp_path / ".endless").mkdir(parents=True, exist_ok=True)
    (tmp_path / ".endless" / "config.json").write_text(
        _json.dumps({"name": "probe", "content": content})
    )
    return tmp_path


def test_absent_config_means_both_gates_on_with_the_builtin_set(tmp_path):
    cfg = project_content_config(tmp_path)
    assert cfg["gates"] == {"absolute_paths": True, "line_citations": True}
    assert cfg["extensions"] == CITATION_EXTENSIONS


def test_block_adds_to_the_default_rather_than_replacing_it(tmp_path):
    root = _project(tmp_path, {"extensions": {"block": ["phtml"]}})
    exts = project_content_config(root)["extensions"]
    assert "phtml" in exts
    assert "go" in exts, "block must layer over the default, not replace it"


def test_unblock_removes_one_entry_and_leaves_the_rest(tmp_path):
    root = _project(tmp_path, {"extensions": {"unblock": ["md"]}})
    exts = project_content_config(root)["extensions"]
    assert "md" not in exts
    assert "go" in exts


def test_a_leading_dot_and_mixed_case_are_accepted(tmp_path):
    root = _project(tmp_path, {"extensions": {"block": [".PHTML"]}})
    assert "phtml" in project_content_config(root)["extensions"]


def test_an_extension_in_both_lists_is_refused_not_resolved(tmp_path):
    """Two settings spelling contradictory intent is the ambiguity --clear
    against a field flag and --status against --keep-status already refuse.
    Picking a winner would let the config file say one thing and the gate do
    another."""
    root = _project(tmp_path, {"extensions": {"block": ["go"], "unblock": ["go"]}})
    with _pytest.raises(ValueError) as exc:
        project_content_config(root)
    assert "go" in str(exc.value)
    assert "both" in str(exc.value)


@_pytest.mark.parametrize("gate", ["absolute_paths", "line_citations"])
def test_either_gate_can_be_switched_off(tmp_path, gate):
    root = _project(tmp_path, {"gates": {gate: False}})
    gates = project_content_config(root)["gates"]
    assert gates[gate] is False
    other = "line_citations" if gate == "absolute_paths" else "absolute_paths"
    assert gates[other] is True, "the switches are independent"


def test_a_malformed_content_block_falls_back_to_the_defaults(tmp_path):
    root = _project(tmp_path, "not-a-dict")
    cfg = project_content_config(root)
    assert cfg["gates"] == {"absolute_paths": True, "line_citations": True}
    assert cfg["extensions"] == CITATION_EXTENSIONS
