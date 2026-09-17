"""The per-worktree sandbox: where it resolves to, and what creating one does.

Sandboxes live inside their worktrees at `<worktree>/.endless/sandbox/`, and
every project's worktrees get one — not only self-dev projects.
"""

import json
import re
import subprocess
from pathlib import Path

from endless import config, worktree_cmd


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


# --------------------------------------------------------------------------- #
# provisioning
# --------------------------------------------------------------------------- #

def test_provisioning_is_universal_and_self_ignoring(tmp_path):
    root = _project(tmp_path, self_dev=False)
    wt = _worktree(root, 1964)
    sandbox = worktree_cmd.provision_worktree_sandbox(wt)
    assert sandbox.is_dir()
    gitignore = (sandbox / ".gitignore").read_text()
    assert "*" in gitignore.splitlines()
    # Empty apart from the ignore file: endless seeds nothing.
    assert sorted(p.name for p in sandbox.iterdir()) == [".gitignore"]


def test_provisioning_keeps_what_is_already_there(tmp_path):
    root = _project(tmp_path)
    wt = _worktree(root, 1964)
    sandbox = worktree_cmd.provision_worktree_sandbox(wt)
    (sandbox / "fixture.db").write_text("payload")
    worktree_cmd.provision_worktree_sandbox(wt)
    assert (sandbox / "fixture.db").read_text() == "payload"


def test_provisioning_restores_a_missing_gitignore(tmp_path):
    """A sandbox that arrived without its .gitignore — relocated from the old
    cache root, say — is visible to git until something writes one."""
    root = _project(tmp_path)
    wt = _worktree(root, 1964)
    sandbox = config.sandbox_root(wt)
    (sandbox / "endless").mkdir(parents=True)
    (sandbox / "endless" / "endless.db").write_text("existing")

    worktree_cmd.provision_worktree_sandbox(wt)

    assert (sandbox / ".gitignore").read_text() == worktree_cmd._SANDBOX_GITIGNORE
    assert (sandbox / "endless" / "endless.db").read_text() == "existing"


def test_the_two_gitignore_constants_agree():
    """Go writes the same file when `sandbox init` provisions one, and a
    worktree provisioned by one half must not show a diff under the other."""
    go = Path("internal/sandboxcmd/worktree_sandbox.go").read_text()
    m = re.search(r"const SandboxGitignore = `(.*?)`", go, re.S)
    assert m, "Go's SandboxGitignore constant not found"
    assert m.group(1) == worktree_cmd._SANDBOX_GITIGNORE


def test_a_provisioned_sandbox_is_invisible_to_git(tmp_path):
    """The self-ignore is what makes adoption free — no project has to add a
    .gitignore entry, and `worktree land` never trips over the sandbox."""
    root = _project(tmp_path)
    wt = _worktree(root, 100)
    for args in (
        ["init", "-q", "-b", "main"],
        ["config", "user.email", "t@example.com"],
        ["config", "user.name", "t"],
    ):
        subprocess.run(["git", "-C", str(wt), *args], check=True,
                       capture_output=True)
    (wt / "README").write_text("x")

    sandbox = worktree_cmd.provision_worktree_sandbox(wt)
    (sandbox / "secrets.env").write_text("TOKEN=shh")
    (sandbox / "endless").mkdir()
    (sandbox / "endless" / "config.json").write_text("{}")

    status = subprocess.run(
        ["git", "-C", str(wt), "status", "--porcelain", "--untracked-files=all"],
        capture_output=True, text=True, check=True,
    ).stdout
    assert "sandbox" not in status, status
    assert "README" in status, "the fixture must otherwise be reporting files"


# --------------------------------------------------------------------------- #
# the refusal
# --------------------------------------------------------------------------- #

def test_the_missing_sandbox_refusal_names_the_remedy(tmp_path):
    root = _project(tmp_path)
    wt = _worktree(root, 1964)
    msg = config.sandbox_missing_refusal(wt, config.sandbox_root(wt))
    assert str(wt) in msg
    assert "just dev-sandbox-init" in msg
    assert "sandbox migrate" not in msg, "that command no longer exists"
