"""Project identity and the machine-local slug (E-2217).

A project is a git repo identified by its root commit. Registration records
that identity in `.endless/config.json`, refuses a second checkout (a clone or
a fork) of a project already registered on this machine, and refuses a name
another registered project holds. The slug is machine-local: it defaults to the
name, survives a rename, and never reaches a committed file.
"""

import json
import os
import subprocess
from pathlib import Path

import pytest

from endless import config, db
from endless.agent_help import Refusal
from endless.project_identity import NoIdentity, identity_for, root_commit
from endless.project_sync import sync_projects
from endless.register import backfill_identities, register_project
from endless.rename import rename_project

from conftest import init_git_repo


def _git(path: Path, *args: str, env: dict | None = None) -> str:
    full_env = dict(os.environ, **(env or {}))
    res = subprocess.run(
        ["git", "-c", "user.email=test@example.com", "-c", "user.name=Test",
         *args],
        cwd=str(path), check=True, capture_output=True, text=True, env=full_env,
    )
    return res.stdout.strip()


def _head(path: Path) -> str:
    return _git(path, "rev-parse", "HEAD")


def _commit_config(path: Path) -> None:
    """Commit the config, unless register's own bring-up commit already did
    (E-2349), so the checkout is clean either way."""
    _git(path, "add", "-A")
    if _git(path, "status", "--porcelain"):
        _git(path, "commit", "-q", "-m", "register")


def _row(name: str):
    rows = db.query(
        "SELECT name, slug, identity, path FROM projects WHERE name = ?",
        (name,),
    )
    return rows[0] if rows else None


# --- deriving the identity ---------------------------------------------------


def test_root_commit_refuses_a_directory_that_is_not_a_repo(tmp_path):
    with pytest.raises(NoIdentity) as e:
        root_commit(tmp_path)
    assert e.value.reason == "not_a_repo"


def test_root_commit_refuses_a_repo_with_no_commits(tmp_path):
    _git(tmp_path, "init", "-q")
    with pytest.raises(NoIdentity) as e:
        root_commit(tmp_path)
    assert e.value.reason == "no_commits"


def test_root_commit_is_the_first_commit_not_head(tmp_path):
    repo = init_git_repo(tmp_path / "r")
    first = _head(repo)
    _git(repo, "commit", "--allow-empty", "-q", "-m", "second")
    assert root_commit(repo) == first


def test_root_commit_picks_the_oldest_of_several_roots(tmp_path):
    """Unrelated histories merged: every clone must pick the same root."""
    repo = tmp_path / "r"
    repo.mkdir()
    _git(repo, "init", "-q", "-b", "main")
    newer = {"GIT_COMMITTER_DATE": "2024-01-01T00:00:00Z",
             "GIT_AUTHOR_DATE": "2024-01-01T00:00:00Z"}
    older = {"GIT_COMMITTER_DATE": "2020-01-01T00:00:00Z",
             "GIT_AUTHOR_DATE": "2020-01-01T00:00:00Z"}
    _git(repo, "commit", "--allow-empty", "-q", "-m", "newer root", env=newer)
    _git(repo, "checkout", "-q", "--orphan", "other")
    _git(repo, "commit", "--allow-empty", "-q", "-m", "older root", env=older)
    older_sha = _head(repo)
    _git(repo, "checkout", "-q", "main")
    _git(repo, "merge", "-q", "--allow-unrelated-histories", "-m", "merge",
         "other")
    assert root_commit(repo) == older_sha


def test_a_stored_identity_wins_over_the_root_commit(tmp_path):
    """A history rewrite changes the root commit; it must not change the
    project's identity."""
    repo = init_git_repo(tmp_path / "r")
    assert identity_for(repo, {"identity": "stored-value"}) == "stored-value"


# --- registering -------------------------------------------------------------


def test_register_records_the_identity_and_defaults_the_slug(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    register_project(repo, infer=True)

    cfg = config.project_config_read(repo)
    assert cfg["identity"] == root_commit(repo)
    row = _row("alpha")
    assert row["identity"] == cfg["identity"]
    assert row["slug"] == "alpha"


def test_register_refuses_a_directory_that_is_not_a_repo(isolated_env):
    plain = isolated_env["projects_root"] / "plain"
    plain.mkdir()
    with pytest.raises(Refusal) as e:
        register_project(plain, infer=True)
    assert "not a git repository" in str(e.value).lower()
    assert not (plain / ".endless").exists(), "a refused register wrote config"
    assert db.query("SELECT 1 FROM projects") == []


def test_register_refuses_a_repo_with_no_commits(isolated_env):
    repo = isolated_env["projects_root"] / "empty"
    repo.mkdir()
    _git(repo, "init", "-q")
    with pytest.raises(Refusal) as e:
        register_project(repo, infer=True)
    assert "no commits yet" in str(e.value).lower()
    assert db.query("SELECT 1 FROM projects") == []


def test_register_refuses_a_second_clone(isolated_env):
    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    register_project(first, infer=True)
    _commit_config(first)
    clone = root / "alpha-clone"
    _git(root, "clone", "-q", str(first), str(clone))

    with pytest.raises(Refusal) as e:
        register_project(clone, name="alpha-two", infer=True)
    message = str(e.value)
    assert str(first) in message, "the refusal must name the registered path"
    assert "worktree" in message
    assert "Forks are not yet supported" in message
    assert [r["name"] for r in db.query("SELECT name FROM projects")] == ["alpha"]
    # Nothing in the clone changed: its config is still the committed one.
    assert _git(clone, "status", "--porcelain") == ""


def test_register_refuses_a_fork_whose_config_was_never_committed(
    isolated_env,
):
    """A fork made before `register` ever ran carries no stored identity; the
    root commit alone still identifies it."""
    root = isolated_env["projects_root"]
    upstream = init_git_repo(root / "upstream")
    fork = root / "fork"
    _git(root, "clone", "-q", str(upstream), str(fork))
    register_project(upstream, infer=True)

    with pytest.raises(Refusal):
        register_project(fork, infer=True)


def test_register_refuses_an_unrelated_project_with_a_clashing_name(
    isolated_env, tmp_path,
):
    first = init_git_repo(isolated_env["projects_root"] / "api")
    register_project(first, infer=True)
    other = init_git_repo(tmp_path / "elsewhere" / "api")

    with pytest.raises(Refusal) as e:
        register_project(other, infer=True)
    assert str(first) in str(e.value)
    assert "--name" in str(e.value)
    assert len(db.query("SELECT 1 FROM projects")) == 1

    register_project(other, name="api-two", infer=True)
    assert _row("api-two")["slug"] == "api-two"


def test_reregistering_the_same_checkout_is_an_update(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    register_project(repo, infer=True)
    register_project(repo, label="Again", infer=True)
    assert len(db.query("SELECT 1 FROM projects")) == 1


def test_reregistering_keeps_a_stored_identity(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    register_project(repo, infer=True)
    cfg = config.project_config_read(repo)
    cfg["identity"] = "from-before-a-rewrite"
    config.project_config_write(repo, cfg)

    register_project(repo, infer=True)
    assert config.project_config_read(repo)["identity"] == "from-before-a-rewrite"
    assert _row("alpha")["identity"] == "from-before-a-rewrite"


# --- the slug ----------------------------------------------------------------


def test_the_slug_survives_a_rename_and_never_reaches_config(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    register_project(repo, infer=True)
    rename_project("alpha", "omega")

    row = _row("omega")
    assert row["slug"] == "alpha"
    on_disk = (repo / ".endless" / "config.json").read_text()
    assert "slug" not in json.loads(on_disk)
    assert "alpha" not in on_disk, "the machine-local slug reached a committed file"


def test_a_renamed_projects_slug_is_not_free_for_another(isolated_env, tmp_path):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    register_project(repo, infer=True)
    rename_project("alpha", "omega")

    other = init_git_repo(tmp_path / "x" / "alpha")
    with pytest.raises(Refusal):
        register_project(other, infer=True)

    beta = init_git_repo(isolated_env["projects_root"] / "beta")
    register_project(beta, infer=True)
    with pytest.raises(Refusal):
        rename_project("beta", "alpha")
    rename_project("omega", "alpha")  # back to its own slug is fine


# --- project sync ---------------------------------------------------------------


def test_sync_does_not_repoint_a_project_at_a_second_clone(isolated_env):
    """Before E-2217 a clone with the same name under the roots silently took
    the registration over from the checkout that holds it."""
    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    register_project(first, infer=True)
    _commit_config(first)
    _git(root, "clone", "-q", str(first), str(root / "zz-alpha-clone"))

    sync_projects()

    rows = db.query("SELECT path FROM projects")
    assert len(rows) == 1
    assert Path(rows[0]["path"]).expanduser() == first.resolve()


def test_sync_skips_a_second_clone_under_another_name(isolated_env):
    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    register_project(first, infer=True)
    _commit_config(first)
    clone = root / "alpha-clone"
    _git(root, "clone", "-q", str(first), str(clone))
    cfg = config.project_config_read(clone)
    cfg["name"] = "alpha-renamed"
    config.project_config_write(clone, cfg)

    sync_projects()

    assert len(db.query("SELECT 1 FROM projects")) == 1


# --- backfill ----------------------------------------------------------------


def _legacy_register(repo: Path, name: str) -> None:
    """A project registered before identities existed: committed config with
    no identity, a row with none."""
    (repo / ".endless").mkdir(exist_ok=True)
    config.project_config_write(repo, {"name": name, "status": "active"})
    _commit_config(repo)
    db.execute(
        "INSERT INTO projects (name, slug, path, status) VALUES (?, ?, ?, 'active')",
        (name, name, str(repo)),
    )


def test_backfill_records_commits_and_is_idempotent(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    _legacy_register(repo, "alpha")
    before = _head(repo)

    backfill_identities()

    expected = root_commit(repo)
    assert config.project_config_read(repo)["identity"] == expected
    assert _row("alpha")["identity"] == expected
    assert _git(repo, "rev-parse", "HEAD~1") == before, "expected one commit"
    assert _git(repo, "show", "--name-only", "--format=", "HEAD") == ".endless/config.json"
    assert _git(repo, "status", "--porcelain") == ""

    head = _head(repo)
    assert backfill_identities() == [("alpha", "already recorded")]
    assert _head(repo) == head


def test_backfill_dry_run_writes_nothing(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    _legacy_register(repo, "alpha")
    head = _head(repo)

    backfill_identities(dry_run=True)

    assert "identity" not in config.project_config_read(repo)
    assert _row("alpha")["identity"] is None
    assert _head(repo) == head


def test_backfill_leaves_a_config_with_its_own_edits_uncommitted(isolated_env):
    repo = init_git_repo(isolated_env["projects_root"] / "alpha")
    _legacy_register(repo, "alpha")
    cfg = config.project_config_read(repo)
    cfg["label"] = "an edit of the user's own"
    config.project_config_write(repo, cfg)
    head = _head(repo)

    backfill_identities()

    assert _head(repo) == head, "the user's edit was swept into a commit"
    assert config.project_config_read(repo)["identity"] == root_commit(repo)
    assert _row("alpha")["identity"] == root_commit(repo)


def test_backfill_reports_two_checkouts_registered_before_the_rule(
    isolated_env,
):
    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    _legacy_register(first, "alpha")
    clone = root / "alpha-clone"
    _git(root, "clone", "-q", str(first), str(clone))
    cfg = config.project_config_read(clone)
    cfg["name"] = "alpha-clone"
    config.project_config_write(clone, cfg)
    _commit_config(clone)
    db.execute(
        "INSERT INTO projects (name, slug, path, status) VALUES "
        "('alpha-clone', 'alpha-clone', ?, 'active')",
        (str(clone),),
    )

    outcomes = dict(backfill_identities())

    assert _row("alpha")["identity"] == root_commit(first)
    assert outcomes["alpha-clone"].startswith("skipped: another checkout of 'alpha'")
    assert _row("alpha-clone")["identity"] is None


# --- moved and unregistered checkouts ----------------------------------------


def test_a_checkout_whose_original_is_gone_takes_the_project_over(
    isolated_env,
):
    """A moved checkout is the same project: register adopts its row, tasks
    and all, instead of refusing or registering a second project."""
    import shutil

    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    register_project(first, infer=True)
    _commit_config(first)
    clone = root / "alpha-moved"
    _git(root, "clone", "-q", str(first), str(clone))
    original_id = db.query("SELECT id FROM projects WHERE name = 'alpha'")[0]["id"]
    shutil.rmtree(first)

    register_project(clone, infer=True)

    rows = db.query("SELECT id, name, path, slug FROM projects")
    assert len(rows) == 1
    assert rows[0]["id"] == original_id
    assert rows[0]["name"] == "alpha"
    assert rows[0]["slug"] == "alpha"
    assert Path(rows[0]["path"]).expanduser() == clone.resolve()


def test_a_clone_of_an_unregistered_project_is_refused_accurately(
    isolated_env,
):
    """An unregistered project keeps its row and tasks (E-2251), so a clone of
    it is still a second checkout, and the refusal says it was unregistered."""
    from endless.unregister import unregister_project

    root = isolated_env["projects_root"]
    first = init_git_repo(root / "alpha")
    register_project(first, infer=True)
    _commit_config(first)
    clone = root / "alpha-clone"
    _git(root, "clone", "-q", str(first), str(clone))
    unregister_project("alpha")

    with pytest.raises(Refusal) as e:
        register_project(clone, name="alpha-two", infer=True)
    assert "unregistered" in str(e.value).lower()
    assert str(first) in str(e.value)

    register_project(first, infer=True)  # the way back still works
    assert _row("alpha")["path"]
