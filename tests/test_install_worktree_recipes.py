"""End-to-end test that a worktree-scoped `just install` takes its machine-level
config recipes from MAIN's justfile and `build` from the branch's (E-2183).

A worktree on an older branch carries an older justfile. A bare `just
claude-settings-init` there ran that branch's version, which for any branch cut
before E-2166 re-pinned every Claude hook at the worktree's own binary. So the
scoped install resolves go-work-init, dev-sandbox-init and claude-settings-init
through `--justfile <main>/justfile`, the way post-worktree-create.sh does —
and leaves `build` bare, because a worktree exists to build candidate code.

WHY END-TO-END. A test that greps the recipe for `--justfile` passes on a recipe
that names the flag and still calls the wrong thing. This one builds a real repo
with a real worktree whose justfile DIFFERS from main's: both carry the real
`install` recipe, copied from this checkout, but each stubs the four recipes it
calls with one that logs which justfile it came from. The log is the assertion.
"""

import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
JUSTFILE = ROOT / "justfile"

pytestmark = pytest.mark.skipif(
    shutil.which("just") is None, reason="the `just` binary is not on PATH"
)

CALLED = ("go-work-init", "build", "dev-sandbox-init", "claude-settings-init")


def _install_recipe() -> str:
    """The real `install` recipe: its header line through its indented body."""
    m = re.search(r'^install mode="":\n(?:(?:[ \t]+.*)?\n)+', JUSTFILE.read_text(),
                  re.MULTILINE)
    assert m, "no install recipe in the justfile"
    return m.group(0)


def _justfile(origin: str, log: Path) -> str:
    stubs = "".join(f"{r}:\n    @echo {origin}:{r} >> {log}\n\n" for r in CALLED)
    return _install_recipe() + "\n" + stubs


def _git(cwd, *args):
    return subprocess.run(
        ["git", "-c", "user.email=t@example.com", "-c", "user.name=T", *args],
        cwd=str(cwd), capture_output=True, text=True, check=True,
    )


@pytest.fixture
def worktree(tmp_path):
    """A repo whose main justfile logs `main:` and whose task branch logs `branch:`."""
    tmp_path = tmp_path.resolve()
    log = tmp_path / "calls.log"
    repo = tmp_path / "proj"
    repo.mkdir()
    _git(repo, "init", "-b", "main")
    (repo / "justfile").write_text(_justfile("branch", log))
    _git(repo, "add", "justfile")
    _git(repo, "commit", "-m", "the branch's justfile")

    wt = repo / ".endless" / "worktrees" / "e-1"
    _git(repo, "worktree", "add", "-b", "task/1", str(wt))

    # Main moves on after the branch was cut — the case that bit E-2166.
    (repo / "justfile").write_text(_justfile("main", log))
    _git(repo, "commit", "-am", "main's newer justfile")
    return wt, log


def test_config_recipes_come_from_main_and_build_from_the_branch(worktree):
    wt, log = worktree
    proc = subprocess.run(["just", "install"], cwd=str(wt),
                          capture_output=True, text=True, env=dict(os.environ))
    assert proc.returncode == 0, proc.stderr

    assert log.read_text().split() == [
        "main:go-work-init",
        "branch:build",
        "main:dev-sandbox-init",
        "main:claude-settings-init",
    ]
