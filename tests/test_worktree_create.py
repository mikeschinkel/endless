"""Tests for E-971 Layer F: per-task worktree auto-creation helpers.

Covers _slugify_title, _default_base_branch, and _check_plan_file_committed.
The end-to-end create_task_worktree flow is exercised via test_task_start_worktree.
"""

import json
import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    _check_plan_file_committed,
    _default_base_branch,
    _slugify_title,
)


# ---------------------------------------------------------------------------
# _slugify_title
# ---------------------------------------------------------------------------

def test_slugify_normal_title():
    out = _slugify_title("Move title verbs from hardcoded list to database table")
    assert out == "move-title-verbs-hardcoded-list-database"


def test_slugify_all_filler_falls_back_to_task():
    assert _slugify_title("The to from") == "task"


def test_slugify_whitespace_only_falls_back_to_task():
    assert _slugify_title("   ") == "task"


def test_slugify_empty_string_falls_back_to_task():
    assert _slugify_title("") == "task"


def test_slugify_drops_punctuation():
    out = _slugify_title("Edit user's profile (UI/UX)")
    assert out == "edit-user-s-profile-ui-ux"


def test_slugify_collapses_repeated_separators():
    out = _slugify_title("foo___bar---baz")
    assert out == "foo-bar-baz"


def test_slugify_truncates_at_word_boundary():
    title = "alpha bravo charlie delta echo foxtrot golf hotel india juliet"
    out = _slugify_title(title)
    assert len(out) <= 40
    assert not out.endswith("-")
    # Confirm we cut at a hyphen, not mid-word
    assert "-" in out
    assert all(part for part in out.split("-"))


def test_slugify_short_title_passes_through():
    assert _slugify_title("Add tests") == "add-tests"


def test_slugify_lowercases():
    assert _slugify_title("FOO BAR") == "foo-bar"


# ---------------------------------------------------------------------------
# _default_base_branch
# ---------------------------------------------------------------------------

def test_default_base_branch_returns_origin_head(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        assert args == ["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"]
        return "refs/remotes/origin/develop"

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "develop"


def test_default_base_branch_falls_back_to_main(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        raise subprocess.CalledProcessError(1, ["git"], stderr="not set")

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "main"


def test_default_base_branch_handles_empty_ref(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        return ""

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "main"


# ---------------------------------------------------------------------------
# _check_plan_file_committed
# ---------------------------------------------------------------------------

def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def git_repo(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)
    return repo


def test_check_plan_file_absent_returns_none(git_repo):
    assert _check_plan_file_committed(1170, git_repo) is None


def test_check_plan_file_committed_clean_returns_none(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    _run(["git", "add", ".endless/plans/E-1170.md"], git_repo)
    _run(["git", "commit", "-q", "-m", "add plan"], git_repo)
    assert _check_plan_file_committed(1170, git_repo) is None


def test_check_plan_file_untracked_returns_message(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    msg = _check_plan_file_committed(1170, git_repo)
    assert msg is not None
    assert ".endless/plans/E-1170.md" in msg
    assert "git -C" in msg
    assert "endless task start E-1170" in msg


def test_check_plan_file_modified_tracked_returns_message(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    _run(["git", "add", ".endless/plans/E-1170.md"], git_repo)
    _run(["git", "commit", "-q", "-m", "add plan"], git_repo)
    (plans / "E-1170.md").write_text("plan content modified\n")
    msg = _check_plan_file_committed(1170, git_repo)
    assert msg is not None
    assert ".endless/plans/E-1170.md" in msg
