"""Tests for matcher seed and migration logic (E-1028)."""

import json
from pathlib import Path

import pytest

from endless import matchers


def _write_machine_config(config_dir: Path, payload: dict) -> Path:
    cfg = config_dir / "config.json"
    cfg.write_text(json.dumps(payload))
    return cfg


def _machine_config(config_dir: Path) -> dict:
    return json.loads((config_dir / "config.json").read_text())


def test_ensure_default_seeds_writes_when_missing(isolated_env):
    cfg = isolated_env["config_dir"] / "config.json"
    # conftest writes a config without matchers
    assert "matchers" not in json.loads(cfg.read_text())

    matchers._ensure_default_seeds()

    data = json.loads(cfg.read_text())
    assert "matchers" in data
    assert any(m.get("type") == "start" and m.get("scope") == "task"
               for m in data["matchers"])


def test_ensure_default_seeds_idempotent(isolated_env):
    matchers._ensure_default_seeds()
    first = _machine_config(isolated_env["config_dir"])
    matchers._ensure_default_seeds()
    second = _machine_config(isolated_env["config_dir"])
    assert first == second


def test_migrate_stale_default_rewrites_start_regex(isolated_env):
    config_dir = isolated_env["config_dir"]
    _write_machine_config(config_dir, {
        "matchers": [
            {
                "type": "start", "scope": "task", "method": "regex",
                "match": r"endless\s+task\s+start\s+(\d+)",
            },
        ],
    })

    matchers._migrate_stale_defaults()

    data = _machine_config(config_dir)
    assert data["matchers"][0]["match"] == r"endless\s+task\s+start\s+(?:[Ee]-)?(\d+)"


def test_migrate_stale_default_rewrites_complete_regex(isolated_env):
    config_dir = isolated_env["config_dir"]
    _write_machine_config(config_dir, {
        "matchers": [
            {
                "type": "complete", "scope": "task", "method": "regex",
                "match": r"endless\s+task\s+complete\s+(\d+)",
            },
        ],
    })

    matchers._migrate_stale_defaults()

    data = _machine_config(config_dir)
    assert data["matchers"][0]["match"] == r"endless\s+task\s+complete\s+(?:[Ee]-)?(\d+)"


def test_migrate_stale_default_leaves_customized_regex(isolated_env):
    """A user-customized regex (different from old default) must not be touched."""
    config_dir = isolated_env["config_dir"]
    custom = r"endless\s+task\s+start\s+(\d+)\s+--force"
    _write_machine_config(config_dir, {
        "matchers": [
            {
                "type": "start", "scope": "task", "method": "regex",
                "match": custom,
            },
        ],
    })

    matchers._migrate_stale_defaults()

    data = _machine_config(config_dir)
    assert data["matchers"][0]["match"] == custom


def test_migrate_stale_default_idempotent(isolated_env):
    config_dir = isolated_env["config_dir"]
    _write_machine_config(config_dir, {
        "matchers": [
            {
                "type": "start", "scope": "task", "method": "regex",
                "match": r"endless\s+task\s+start\s+(\d+)",
            },
        ],
    })

    matchers._migrate_stale_defaults()
    first = _machine_config(config_dir)
    matchers._migrate_stale_defaults()
    second = _machine_config(config_dir)
    assert first == second


def test_load_all_matchers_runs_migration(isolated_env):
    """load_all_matchers calls _migrate_stale_defaults on every invocation."""
    config_dir = isolated_env["config_dir"]
    _write_machine_config(config_dir, {
        "matchers": [
            {
                "type": "start", "scope": "task", "method": "regex",
                "match": r"endless\s+task\s+start\s+(\d+)",
            },
        ],
    })

    matchers.load_all_matchers()

    data = _machine_config(config_dir)
    assert data["matchers"][0]["match"] == r"endless\s+task\s+start\s+(?:[Ee]-)?(\d+)"


def test_default_seed_uses_eprefix_pattern(isolated_env):
    """Fresh seed should produce E-prefix-tolerant regex (not the old form)."""
    matchers._ensure_default_seeds()
    data = _machine_config(isolated_env["config_dir"])
    start = next(m for m in data["matchers"]
                 if m.get("type") == "start" and m.get("scope") == "task")
    assert "(?:[Ee]-)?" in start["match"]
    complete = next(m for m in data["matchers"]
                    if m.get("type") == "complete" and m.get("scope") == "task")
    assert "(?:[Ee]-)?" in complete["match"]
