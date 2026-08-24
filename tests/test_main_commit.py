"""Tests for main_commit — the shared write-time single-file commit (E-2055).

The mechanics were `matchers._commit_project_verbs`'s private business until
`endless lesson write` needed exactly the same thing; they moved here so the two
callers cannot drift. The env sanitization test came with them from
test_matchers.py, where it guarded the same code under its old name (E-1309).
"""

import subprocess

import pytest

from endless import main_commit


def _git(args, cwd):
    return subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True, cwd=str(cwd),
    ).stdout.rstrip("\n")


@pytest.fixture
def repo(tmp_path):
    """A git repo with one commit and one tracked file."""
    _git(["init", "-q", "-b", "main"], cwd=tmp_path)
    _git(["config", "user.email", "test@example.com"], cwd=tmp_path)
    _git(["config", "user.name", "Test"], cwd=tmp_path)
    _git(["config", "commit.gpgsign", "false"], cwd=tmp_path)
    (tmp_path / "tracked.txt").write_text("one\n")
    _git(["add", "tracked.txt"], cwd=tmp_path)
    _git(["commit", "-q", "-m", "init"], cwd=tmp_path)
    return tmp_path


def test_sanitized_git_env_strips_redirect_vars(monkeypatch):
    """E-1309: the git-locating vars are dropped so a stray GIT_DIR in the
    caller chain can't silently redirect the commit to a worktree's gitdir."""
    for k in main_commit.GIT_REDIRECT_VARS:
        monkeypatch.setenv(k, f"sentinel-{k}")
    monkeypatch.setenv("HOME_E1309_PROBE", "kept")

    env = main_commit.sanitized_git_env()

    for k in main_commit.GIT_REDIRECT_VARS:
        assert k not in env, f"sanitized env leaked {k}"
    assert env.get("HOME_E1309_PROBE") == "kept"


def test_commit_path_commits_only_that_path(repo):
    """Other dirt is left exactly as it was — staged stays staged."""
    (repo / "tracked.txt").write_text("one\ntwo\n")
    (repo / "other.txt").write_text("user work in flight\n")
    _git(["add", "other.txt"], cwd=repo)

    main_commit.commit_path(repo, "tracked.txt", "Endless: subject", "the body")

    assert _git(["log", "-1", "--format=%s"], cwd=repo) == "Endless: subject"
    assert _git(["log", "-1", "--format=%b"], cwd=repo).strip() == "the body"
    assert _git(["show", "--name-only", "--format=", "HEAD"], cwd=repo) == "tracked.txt"
    assert _git(["status", "--porcelain", "--", "other.txt"], cwd=repo).startswith("A ")


def test_commit_path_adds_an_untracked_file(repo):
    """`commit -o` alone fails with a pathspec error on a brand-new file; the
    `git add` step is what makes the first-ever write committable."""
    (repo / "brand-new.txt").write_text("first\n")

    main_commit.commit_path(repo, "brand-new.txt", "Endless: new file")

    assert _git(["show", "--name-only", "--format=", "HEAD"], cwd=repo) == "brand-new.txt"


def test_commit_path_omits_an_empty_body(repo):
    (repo / "tracked.txt").write_text("changed\n")

    main_commit.commit_path(repo, "tracked.txt", "Endless: no body", "   ")

    assert _git(["log", "-1", "--format=%b"], cwd=repo).strip() == ""


def test_commit_path_raises_on_git_failure(repo):
    """Nothing staged for that path means nothing to commit — a git failure the
    caller must surface rather than swallow."""
    with pytest.raises(RuntimeError) as exc:
        main_commit.commit_path(repo, "tracked.txt", "Endless: no change")
    assert "git commit failed" in str(exc.value)
