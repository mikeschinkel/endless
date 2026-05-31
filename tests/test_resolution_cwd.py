"""Tests for config.resolution_cwd — main-checkout-aware project resolution
under --db main from inside a self-dev worktree (E-1519 Part C)."""

import os
import subprocess
from pathlib import Path

import pytest

from endless import config


def _init_git_with_worktree(tmp_path: Path) -> tuple[Path, Path]:
    """Create a main checkout (git repo) and a linked worktree under
    .endless/worktrees/e-9999. Returns (main_checkout, worktree)."""
    main = tmp_path / "project"
    main.mkdir()
    subprocess.run(["git", "-C", str(main), "init", "-q", "-b", "main"], check=True)
    subprocess.run(["git", "-C", str(main), "config", "user.email", "t@e"], check=True)
    subprocess.run(["git", "-C", str(main), "config", "user.name", "t"], check=True)
    subprocess.run(["git", "-C", str(main), "config", "commit.gpgsign", "false"], check=True)
    (main / "README").write_text("x\n")
    subprocess.run(["git", "-C", str(main), "add", "README"], check=True)
    subprocess.run(["git", "-C", str(main), "commit", "-q", "-m", "init"], check=True)

    worktree = main / ".endless" / "worktrees" / "e-9999"
    worktree.parent.mkdir(parents=True)
    subprocess.run(
        ["git", "-C", str(main), "worktree", "add", "-q", "-b", "task/9999", str(worktree)],
        check=True,
    )
    return main.resolve(), worktree.resolve()


def test_returns_cwd_when_not_main_db(tmp_path, monkeypatch):
    """If RESOLVED_CONFIG_DIR isn't the main config dir, never walk."""
    main, worktree = _init_git_with_worktree(tmp_path)
    monkeypatch.chdir(worktree)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", tmp_path / "sandbox-config")

    result = config.resolution_cwd()

    assert result == worktree


def test_walks_to_main_checkout_under_db_main(tmp_path, monkeypatch):
    main, worktree = _init_git_with_worktree(tmp_path)
    monkeypatch.chdir(worktree)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())

    result = config.resolution_cwd()

    assert result == main


def test_returns_cwd_from_main_checkout(tmp_path, monkeypatch):
    """When already in the main checkout, the helper is a no-op."""
    main, _ = _init_git_with_worktree(tmp_path)
    monkeypatch.chdir(main)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())

    result = config.resolution_cwd()

    assert result == main


def test_returns_cwd_outside_git_repo(tmp_path, monkeypatch):
    """git rev-parse fails cleanly outside a repo; helper falls back to cwd."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())

    result = config.resolution_cwd()

    assert result.resolve() == tmp_path.resolve()
