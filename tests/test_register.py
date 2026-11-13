"""Tests for register and related commands."""

import json
from pathlib import Path

from endless import db, config
from endless.register import register_project, validate_name

from conftest import init_git_repo


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
    init_git_repo(project_dir)

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
    init_git_repo(project_dir)

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
    init_git_repo(project_dir)

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
    init_git_repo(project_dir)

    register_project(project_dir, infer=True)

    row = db.query(
        "SELECT group_name FROM projects WHERE name=?",
        ("sub-proj",),
    )[0]
    assert row["group_name"] == "my-group"


# --- E-2620: init's commits all carry lefthook.yml ---------------------------

LEFTHOOK_SUBJECT = "Endless: set up lefthook for the Endless-Task trailer"


def _git(path: Path, *args: str) -> str:
    import subprocess
    return subprocess.run(
        ["git", "-c", "user.email=test@example.com", "-c", "user.name=Test",
         *args],
        cwd=str(path), check=True, capture_output=True, text=True,
    ).stdout


def _init_with_lefthook(isolated_env, monkeypatch, name: str):
    """Run project init in a fresh repo with lefthook chosen. lefthook itself
    is hidden, so no hooks are installed: the test needs only its files."""
    import shutil
    from endless import git_hook
    real = shutil.which
    monkeypatch.setattr(git_hook.shutil, "which",
                        lambda n, *a, **k: None if n == "lefthook"
                        else real(n, *a, **k))
    proj = init_git_repo(isolated_env["projects_root"] / name)
    _git(proj, "config", "user.email", "test@example.com")
    _git(proj, "config", "user.name", "Test")
    base = _git(proj, "rev-parse", "HEAD").strip()
    register_project(proj, infer=True, git_hooks="lefthook")
    return proj, base


def test_init_commits_lefthook_yml_first(isolated_env, monkeypatch):
    proj, base = _init_with_lefthook(isolated_env, monkeypatch, "lh-first")
    commits = _git(proj, "rev-list", "--reverse", f"{base}..HEAD").split()
    assert len(commits) >= 2
    first = commits[0]
    assert _git(proj, "log", "-1", "--format=%s", first).strip() == LEFTHOOK_SUBJECT
    assert _git(proj, "show", "--name-only", "--format=", first).split() == [
        "lefthook.yml"]
    # Every later init commit holds it too, and nothing is left over.
    for c in commits:
        assert "lefthook.yml" in _git(proj, "ls-tree", "--name-only", c).split()
    assert _git(proj, "status", "--porcelain", "lefthook.yml") == ""


def test_rebased_init_commits_see_lefthook_yml(isolated_env, monkeypatch):
    """The go-filesys case: init's commits replayed by `git pull --rebase` onto
    a remote's unrelated initial commit. prepare-commit-msg (where lefthook's
    hook sync looks for its config) must find lefthook.yml for every one."""
    proj, base = _init_with_lefthook(isolated_env, monkeypatch, "lh-rebase")
    count = len(_git(proj, "rev-list", f"{base}..HEAD").split())

    _git(proj, "checkout", "-q", "--orphan", "remote")
    _git(proj, "rm", "-rq", "--cached", ".")
    (proj / "README.md").write_text("# remote\n")
    _git(proj, "add", "README.md")
    _git(proj, "commit", "-q", "-m", "Initial commit")
    (proj / "README.md").unlink()
    _git(proj, "checkout", "-q", "-f", "main")

    seen = proj / ".git" / "seen"
    hook = proj / ".git" / "hooks" / "prepare-commit-msg"
    hook.write_text("#!/bin/sh\n"
                    "if [ -f lefthook.yml ]; then echo yes; else echo no; fi"
                    f" >> '{seen}'\n")
    hook.chmod(0o755)

    _git(proj, "rebase", "-q", "--onto", "remote", base, "main")
    assert seen.read_text().split() == ["yes"] * count


def test_a_lefthook_yml_with_user_changes_is_left_out_once(isolated_env,
                                                           monkeypatch, capsys):
    import shutil
    from endless import git_hook
    real = shutil.which
    monkeypatch.setattr(git_hook.shutil, "which",
                        lambda n, *a, **k: None if n == "lefthook"
                        else real(n, *a, **k))
    proj = init_git_repo(isolated_env["projects_root"] / "lh-dirty")
    _git(proj, "config", "user.email", "test@example.com")
    _git(proj, "config", "user.name", "Test")
    (proj / "lefthook.yml").write_text("# mine\n")
    _git(proj, "add", "lefthook.yml")
    _git(proj, "commit", "-q", "-m", "user lefthook")
    (proj / "lefthook.yml").write_text("# mine, edited\n")  # uncommitted
    base = _git(proj, "rev-parse", "HEAD").strip()

    register_project(proj, infer=True, git_hooks="lefthook")

    subjects = _git(proj, "log", "--format=%s", f"{base}..HEAD").splitlines()
    assert LEFTHOOK_SUBJECT not in subjects
    assert "lefthook.yml" in _git(proj, "status", "--porcelain")
    out = capsys.readouterr().out
    assert out.count("Not committed: lefthook.yml (it had uncommitted "
                     "changes of yours)") == 1
