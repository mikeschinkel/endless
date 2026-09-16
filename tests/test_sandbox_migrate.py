"""The per-worktree sandbox: where it resolves to, and migrating onto it.

Sandboxes moved from `~/.cache/endless/sandboxes/<name>` into the worktree
itself, and became universal rather than self-dev-only. These are the durable
tests for both halves — the resolver, and the one-shot `endless sandbox migrate`
that brings an existing installation across.
"""

import json
from pathlib import Path

import pytest

from endless import config, sandbox_cmd, worktree_cmd


# --------------------------------------------------------------------------- #
# fixtures
# --------------------------------------------------------------------------- #

def _project(tmp_path, *, self_dev=True, sandbox_root=None):
    """A project checkout with a .endless/config.json."""
    root = tmp_path / "proj"
    (root / ".endless").mkdir(parents=True)
    cfg = {"self_dev": self_dev}
    if sandbox_root is not None:
        cfg["sandbox_root"] = str(sandbox_root)
    (root / ".endless" / "config.json").write_text(json.dumps(cfg) + "\n")
    return root


def _worktree(root, task_id):
    wt = root / ".endless" / "worktrees" / f"e-{task_id}"
    wt.mkdir(parents=True)
    return wt


def _legacy(tmp_path, name, *, payload="payload"):
    """A sandbox in the pre-relocation cache root, with a file in it."""
    d = tmp_path / "cache" / "endless" / "sandboxes" / name
    (d / "endless").mkdir(parents=True)
    (d / "endless" / "endless.db").write_text(payload)
    return d


def _hook(root, body="#!/bin/sh\nexit 0\n"):
    h = root / ".endless" / "hooks" / "post-worktree-create.sh"
    h.parent.mkdir(parents=True, exist_ok=True)
    h.write_text(body)
    h.chmod(0o755)
    return h


@pytest.fixture
def cache(tmp_path, monkeypatch):
    """Point the legacy cache root at tmp so nothing touches the real one."""
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / "cache"))
    return tmp_path / "cache"


# --------------------------------------------------------------------------- #
# the resolver
# --------------------------------------------------------------------------- #

def test_sandbox_resolves_inside_its_worktree(tmp_path):
    root = _project(tmp_path)
    wt = _worktree(root, 1964)
    assert config.sandbox_root(wt) == wt / ".endless" / "sandbox"
    assert config.sandbox_config_dir(wt) == wt / ".endless" / "sandbox" / "endless"


def test_resolver_reads_no_environment(tmp_path, monkeypatch):
    """The path is composition, not injection: no variable can redirect it."""
    root = _project(tmp_path)
    wt = _worktree(root, 1964)
    before = config.sandbox_root(wt)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "elsewhere"))
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / "other"))
    monkeypatch.setenv("ENDLESS_SANDBOX", str(tmp_path / "hijack"))
    assert config.sandbox_root(wt) == before


def test_project_override_moves_sandboxes_out_of_tree(tmp_path):
    out = tmp_path / "outside"
    root = _project(tmp_path, sandbox_root=out)
    wt = _worktree(root, 1964)
    assert config.sandbox_root(wt) == out / "e-1964"


def test_override_expands_a_leading_tilde(tmp_path):
    root = _project(tmp_path, sandbox_root="~/sandboxes")
    wt = _worktree(root, 1964)
    assert config.sandbox_root(wt) == Path.home() / "sandboxes" / "e-1964"


def test_a_non_self_dev_project_still_has_sandboxes(tmp_path):
    """self_dev gates endless's own DB routing, not whether a sandbox exists."""
    root = _project(tmp_path, self_dev=False)
    wt = _worktree(root, 1964)
    assert config.sandbox_root(wt) == wt / ".endless" / "sandbox"


def test_provisioning_is_universal_and_self_ignoring(tmp_path):
    root = _project(tmp_path, self_dev=False)
    wt = _worktree(root, 1964)
    sandbox = worktree_cmd.provision_worktree_sandbox(wt)
    assert sandbox.is_dir()
    gitignore = (sandbox / ".gitignore").read_text()
    assert "*" in gitignore.splitlines()
    # Empty apart from the ignore file: endless seeds nothing.
    assert sorted(p.name for p in sandbox.iterdir()) == [".gitignore"]


def test_the_two_gitignore_constants_agree():
    """Go writes the same file when `sandbox init` provisions one, and a
    worktree provisioned by one half must not show a diff under the other."""
    import re

    go = Path("internal/sandboxcmd/worktree_sandbox.go").read_text()
    m = re.search(r"const SandboxGitignore = `(.*?)`", go, re.S)
    assert m, "Go's SandboxGitignore constant not found"
    assert m.group(1) == worktree_cmd._SANDBOX_GITIGNORE


# --------------------------------------------------------------------------- #
# migration
# --------------------------------------------------------------------------- #

def test_migrate_relocates_by_rename_preserving_contents(tmp_path, cache, monkeypatch):
    root = _project(tmp_path)
    wt = _worktree(root, 100)
    legacy = _legacy(tmp_path, "e-100", payload="the original bytes")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert not legacy.exists()
    moved = config.sandbox_root(wt)
    assert (moved / "endless" / "endless.db").read_text() == "the original bytes"


def test_migrate_refuses_to_guess_when_both_exist(tmp_path, cache, monkeypatch, capsys):
    """Two directories each claiming to be the task's state is a guess; a
    hand-inspected case beats a guessed one."""
    root = _project(tmp_path)
    wt = _worktree(root, 100)
    legacy = _legacy(tmp_path, "e-100", payload="old")
    existing = config.sandbox_root(wt)
    existing.mkdir(parents=True)
    (existing / "marker").write_text("new")
    monkeypatch.chdir(root)

    with pytest.raises(SystemExit) as e:
        sandbox_cmd.migrate_sandboxes(dry_run=False)
    assert e.value.code == 1

    # Neither was touched.
    assert (legacy / "endless" / "endless.db").read_text() == "old"
    assert (existing / "marker").read_text() == "new"
    out = capsys.readouterr()
    assert "COLLISION" in out.out + out.err


def test_migrate_reports_orphans_and_deletes_nothing(tmp_path, cache, monkeypatch, capsys):
    root = _project(tmp_path)
    orphan = _legacy(tmp_path, "e-999")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert orphan.is_dir(), "an orphan must survive migration"
    assert "orphan" in capsys.readouterr().out


def test_migrate_provisions_worktrees_that_never_had_one(tmp_path, cache, monkeypatch):
    """The 3b case. Nothing provisions on a miss any more, so a worktree this
    pass skips is one whose every command refuses from now on."""
    root = _project(tmp_path)
    wt = _worktree(root, 200)
    marker = tmp_path / "hook-ran"
    _hook(root, f"#!/bin/sh\necho ran > {marker}\n")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    sandbox = config.sandbox_root(wt)
    assert sandbox.is_dir()
    assert (sandbox / ".gitignore").exists()
    assert marker.exists(), "the project's bootstrap hook must run"


def test_a_failing_hook_keeps_the_sandbox_and_is_reported(
    tmp_path, cache, monkeypatch, capsys,
):
    root = _project(tmp_path)
    wt_bad = _worktree(root, 300)
    wt_ok = _worktree(root, 301)
    _hook(root, "#!/bin/sh\nexit 3\n")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    # The directory stays, and the sweep carried on to the next worktree.
    assert config.sandbox_root(wt_bad).is_dir()
    assert config.sandbox_root(wt_ok).is_dir()
    out = capsys.readouterr()
    assert "hook failure" in out.out
    assert "hook failed" in out.err


def _summary(out: str) -> str:
    """The report's counts line. The hook runner prints as it goes, so the
    summary is not reliably the first line."""
    for line in out.splitlines():
        if "moved," in line and "provisioned," in line:
            return line
    raise AssertionError(f"no summary line in:\n{out}")


def test_migrate_is_idempotent(tmp_path, cache, monkeypatch, capsys):
    root = _project(tmp_path)
    _worktree(root, 100)
    _legacy(tmp_path, "e-100")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)
    capsys.readouterr()
    sandbox_cmd.migrate_sandboxes(dry_run=False)

    line = _summary(capsys.readouterr().out)
    assert line.startswith("0 moved, 0 provisioned"), line


def test_dry_run_changes_nothing_and_predicts_the_real_run(
    tmp_path, cache, monkeypatch, capsys,
):
    root = _project(tmp_path)
    wt_move = _worktree(root, 100)
    _worktree(root, 200)
    legacy = _legacy(tmp_path, "e-100")
    _hook(root)
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=True)
    dry = _summary(capsys.readouterr().out)

    # Nothing moved, nothing created.
    assert legacy.is_dir()
    assert not config.sandbox_root(wt_move).exists()
    assert dry.startswith("DRY RUN — nothing changed. ")

    sandbox_cmd.migrate_sandboxes(dry_run=False)
    real = _summary(capsys.readouterr().out)

    # The prediction and the outcome are the same counts.
    assert dry[len("DRY RUN — nothing changed. "):] == real
    assert real.startswith("1 moved, 1 provisioned")


def test_migrate_strips_the_dead_env_injection(tmp_path, cache, monkeypatch):
    """`sandbox bind` wrote XDG_CONFIG_HOME into every worktree's Claude
    settings. After relocation that value names a directory that is no longer
    there, and a process whose cwd is outside the worktree would follow it and
    create a fresh empty config at the vacated path."""
    root = _project(tmp_path)
    wt = _worktree(root, 100)
    legacy = _legacy(tmp_path, "e-100")
    settings = wt / ".claude" / "settings.json"
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({
        "env": {"XDG_CONFIG_HOME": str(legacy)},
        "enabledPlugins": ["keep-me"],
    }))
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    data = json.loads(settings.read_text())
    assert "env" not in data, "an env block with nothing left in it is removed"
    assert data["enabledPlugins"] == ["keep-me"], "other keys survive"


def test_a_developers_own_xdg_setting_is_left_alone(tmp_path, cache, monkeypatch):
    """Only a value under the legacy sandbox root is endless's injection."""
    root = _project(tmp_path)
    wt = _worktree(root, 100)
    mine = str(tmp_path / "my-own-config")
    settings = wt / ".claude" / "settings.json"
    settings.parent.mkdir(parents=True)
    settings.write_text(json.dumps({"env": {"XDG_CONFIG_HOME": mine}}))
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert json.loads(settings.read_text())["env"]["XDG_CONFIG_HOME"] == mine


def test_migrate_needs_no_database(tmp_path, cache, monkeypatch):
    """It has to run in a worktree whose sandbox is missing — the one state
    where every database-touching command refuses."""
    root = _project(tmp_path)
    _worktree(root, 100)
    monkeypatch.chdir(root)
    monkeypatch.setattr(
        worktree_cmd, "_project_root",
        lambda: pytest.fail("migrate must not resolve the project via the DB"),
    )
    sandbox_cmd.migrate_sandboxes(dry_run=False)


def test_migrate_outside_a_project_says_so(tmp_path, cache, monkeypatch):
    import click

    monkeypatch.chdir(tmp_path)
    with pytest.raises(click.ClickException) as e:
        sandbox_cmd.migrate_sandboxes(dry_run=False)
    assert "not inside an endless project" in str(e.value)


def test_migrate_honours_the_project_override(tmp_path, cache, monkeypatch):
    out = tmp_path / "outside"
    root = _project(tmp_path, sandbox_root=out)
    wt = _worktree(root, 100)
    _legacy(tmp_path, "e-100", payload="moved")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert (out / "e-100" / "endless" / "endless.db").read_text() == "moved"
    assert not (wt / ".endless" / "sandbox").exists()


def test_a_non_canonical_worktree_dir_is_not_provisioned(tmp_path, cache, monkeypatch):
    """Only `e-NNN` is a task worktree; anything else under worktrees/ is
    somebody's own directory and is left alone."""
    root = _project(tmp_path)
    stray = root / ".endless" / "worktrees" / "scratch"
    stray.mkdir(parents=True)
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert not (stray / ".endless" / "sandbox").exists()


def test_a_file_in_the_legacy_root_is_not_mistaken_for_a_sandbox(
    tmp_path, cache, monkeypatch,
):
    root = _project(tmp_path)
    legacy_root = tmp_path / "cache" / "endless" / "sandboxes"
    legacy_root.mkdir(parents=True)
    stray = legacy_root / "notes.txt"
    stray.write_text("hi")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)

    assert stray.read_text() == "hi"


def test_migrated_sandbox_is_invisible_to_git(tmp_path, cache, monkeypatch):
    """The self-ignore is what makes adoption free — no project has to add a
    .gitignore entry, and `worktree land` never trips over the sandbox."""
    import subprocess

    root = _project(tmp_path)
    wt = _worktree(root, 100)
    for args in (
        ["init", "-q", "-b", "main"],
        ["config", "user.email", "t@example.com"],
        ["config", "user.name", "t"],
        ["config", "commit.gpgsign", "false"],
    ):
        subprocess.run(["git", "-C", str(wt), *args], check=True,
                       capture_output=True)
    (wt / "README").write_text("x")
    monkeypatch.chdir(root)

    sandbox_cmd.migrate_sandboxes(dry_run=False)
    (config.sandbox_root(wt) / "secrets.env").write_text("TOKEN=shh")

    status = subprocess.run(
        ["git", "-C", str(wt), "status", "--porcelain", "--untracked-files=all"],
        capture_output=True, text=True, check=True,
    ).stdout
    assert "sandbox" not in status, status
    assert "README" in status, "the fixture must otherwise be reporting files"
