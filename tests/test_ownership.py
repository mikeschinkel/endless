"""Tests for ownership filtering."""

import json
import subprocess
from pathlib import Path
from unittest.mock import patch

from endless import config
from endless.ownership import get_repo_id, is_mine


def test_get_repo_id_ssh_format(tmp_path):
    # Create a fake git repo with SSH remote
    repo = tmp_path / "repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    subprocess.run(
        ["git", "remote", "add", "origin",
         "git@github.com:mikeschinkel/my-project.git"],
        cwd=str(repo), capture_output=True,
    )
    assert get_repo_id(repo) == "github.com/mikeschinkel/my-project"


def test_get_repo_id_https_format(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    subprocess.run(
        ["git", "remote", "add", "origin",
         "https://github.com/charmbracelet/bubbletea.git"],
        cwd=str(repo), capture_output=True,
    )
    assert get_repo_id(repo) == "github.com/charmbracelet/bubbletea"


def test_get_repo_id_no_remote(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    assert get_repo_id(repo) is None


def test_get_repo_id_not_a_repo(tmp_path):
    d = tmp_path / "not-a-repo"
    d.mkdir()
    assert get_repo_id(d) is None


def test_is_mine_with_matching_pattern(isolated_env, tmp_path):
    # Configure ownership
    cfg = config.load_config()
    cfg["ownership"] = {"mine": ["github.com/mikeschinkel/*"]}
    config.save_config(cfg)

    # Create repo with matching remote
    repo = tmp_path / "my-repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    subprocess.run(
        ["git", "remote", "add", "origin",
         "git@github.com:mikeschinkel/my-repo.git"],
        cwd=str(repo), capture_output=True,
    )
    assert is_mine(repo) is True


def test_is_mine_with_non_matching_pattern(isolated_env, tmp_path):
    cfg = config.load_config()
    cfg["ownership"] = {"mine": ["github.com/mikeschinkel/*"]}
    config.save_config(cfg)

    repo = tmp_path / "other-repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    subprocess.run(
        ["git", "remote", "add", "origin",
         "git@github.com:charmbracelet/bubbletea.git"],
        cwd=str(repo), capture_output=True,
    )
    assert is_mine(repo) is False


def test_is_mine_no_remote_is_mine(isolated_env, tmp_path):
    cfg = config.load_config()
    cfg["ownership"] = {"mine": ["github.com/mikeschinkel/*"]}
    config.save_config(cfg)

    repo = tmp_path / "local-only"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    # No remote → assumed mine
    assert is_mine(repo) is True


def test_is_mine_no_ownership_config(isolated_env, tmp_path):
    # No ownership rules → everything is mine (backwards compatible)
    repo = tmp_path / "any-repo"
    repo.mkdir()
    subprocess.run(
        ["git", "init"], cwd=str(repo),
        capture_output=True,
    )
    subprocess.run(
        ["git", "remote", "add", "origin",
         "git@github.com:someone/something.git"],
        cwd=str(repo), capture_output=True,
    )
    assert is_mine(repo) is True
