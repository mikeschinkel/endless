"""End-to-end tests for the `claude-settings-sweep` Justfile recipe (E-2166).

The sweep is the remediation half of E-2166. The generator half stops NEW
worktrees being born with their Claude hooks pinned at their own
`bin/endless-go`; the sweep reaches the ones that already are. It cannot ride in
as a commit, which is why it exists at all: `bin/` and
`.claude/settings.local.json` are both git-ignored, so neither a rebase nor
`endless worktree sync` can deliver a fix to them, and a `settings.local.json`
naming a stale binary outranks anything committed.

WHY THESE ARE END-TO-END. The sweep has no logic of its own worth unit-testing:
it enumerates worktrees and re-runs `claude-settings-init` over each, the same
way `.endless/hooks/post-worktree-create.sh` invokes it at worktree birth. What
can break is the wiring — the enumeration, the refusal, the fail-fast stop — and
none of that is visible from anything smaller than a real repo with real
worktrees. So each test builds one under tmp_path, with a fake $HOME so the
recipe's user-scope hook check has something to find and the real one is never
read.

THE FAIL-FAST BEHAVIOUR IS THE POINT, and is asserted directly. A sweep that
logged a failure and carried on would leave an unknown number of worktrees
pinned to a stale binary while reporting success — the same silent degrade that
made E-2166 necessary, since a stale pinned binary aborts before registering the
session and has twice resurrected dropped schema objects. Stopping on the first
failure is what makes "the sweep succeeded" mean every worktree was swept.
"""

import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
JUSTFILE = ROOT / "justfile"

pytestmark = pytest.mark.skipif(
    shutil.which("just") is None, reason="the `just` binary is not on PATH"
)

STUB_BINARY = "#!/bin/sh\nexit 0\n"

USER_SETTINGS = {
    "hooks": {
        "PreToolUse": [
            {"hooks": [{"command": "/usr/local/bin/endless-go hook claude",
                        "type": "command", "async": False}]}
        ]
    }
}


def _git(cwd, *args):
    return subprocess.run(
        ["git", "-c", "user.email=t@example.com", "-c", "user.name=T", *args],
        cwd=str(cwd), capture_output=True, text=True, check=True,
    )


def _pin(worktree: Path):
    """Write the hooks block claude-settings-init used to produce."""
    cmd = f"{worktree}/bin/endless-go hook claude"
    claude = worktree / ".claude"
    claude.mkdir(parents=True, exist_ok=True)
    (claude / "settings.local.json").write_text(json.dumps({
        "hooks": {
            "PreToolUse": [{"hooks": [{"command": cmd, "type": "command",
                                       "async": False}]}],
        },
        "worktree": {"bgIsolation": "none"},
    }, indent=2) + "\n")


def _is_pinned(worktree: Path) -> bool:
    f = worktree / ".claude" / "settings.local.json"
    if not f.is_file():
        return False
    return f"{worktree}/bin/endless-go" in f.read_text()


@pytest.fixture
def project(tmp_path):
    """A git repo with three pinned task worktrees and a fake $HOME.

    `.resolve()` matters: on macOS tmp_path is under a symlinked /var, and the
    sweep compares the paths `git worktree list` recorded against its own
    `$(pwd)`. Resolving up front keeps both spellings the same, as they are in
    a real checkout.
    """
    tmp_path = tmp_path.resolve()
    repo = tmp_path / "proj"
    repo.mkdir()
    _git(repo, "init", "-b", "main")
    shutil.copy(JUSTFILE, repo / "justfile")
    _git(repo, "add", "justfile")
    _git(repo, "commit", "-m", "init")

    worktrees = []
    for n in (1, 2, 3):
        wt = repo / ".endless" / "worktrees" / f"e-{n}"
        _git(repo, "worktree", "add", "-b", f"task/{n}", str(wt))
        # A stub binary so the recipe's legacy skip-worktree de-arm finds one
        # here and never reaches the real endless-go.
        (wt / "bin").mkdir(parents=True, exist_ok=True)
        stub = wt / "bin" / "endless-go"
        stub.write_text(STUB_BINARY)
        stub.chmod(0o755)
        _pin(wt)
        worktrees.append(wt)

    home = tmp_path / "home"
    (home / ".claude").mkdir(parents=True)
    (home / ".claude" / "settings.json").write_text(json.dumps(USER_SETTINGS))

    return repo, worktrees, home


def _sweep(repo, home, *, cwd=None):
    env = dict(os.environ, HOME=str(home))
    return subprocess.run(
        ["just", "--justfile", str(repo / "justfile"),
         "--working-directory", str(cwd or repo), "claude-settings-sweep"],
        capture_output=True, text=True, env=env,
    )


def test_every_worktree_is_unpinned(project):
    repo, worktrees, home = project
    assert all(_is_pinned(wt) for wt in worktrees), "fixture did not pin"

    proc = _sweep(repo, home)
    assert proc.returncode == 0, proc.stderr

    for wt in worktrees:
        assert not _is_pinned(wt), f"{wt} is still pinned"
        settings = json.loads((wt / ".claude" / "settings.local.json").read_text())
        assert "hooks" not in settings
        assert settings["worktree"] == {"bgIsolation": "none"}


def test_it_reports_what_it_changed(project):
    repo, worktrees, home = project
    proc = _sweep(repo, home)
    assert "3 worktree(s) visited, 3 unpinned" in proc.stdout, proc.stdout

    # A second pass visits the same three and changes none of them: the count
    # is of pins removed, not of files written.
    again = _sweep(repo, home)
    assert again.returncode == 0, again.stderr
    assert "3 worktree(s) visited, 0 unpinned" in again.stdout, again.stdout


def test_it_stops_on_the_first_failure(project):
    """A worktree it cannot rewrite stops the sweep, named, with a non-zero exit.

    The induced failure is a truncated settings.local.json, which is both a
    realistic way for one to break and the one that leaves the pin in place for
    this test to still see. Which of the three worktrees it is depends on the
    order `git worktree list` reports, and the assertions do not care: what must
    hold is that the sweep stopped and said where, rather than reporting success
    over a tree it only partly swept.
    """
    repo, worktrees, home = project
    broken = worktrees[1]
    (broken / ".claude" / "settings.local.json").write_text(
        '{"hooks": [ "%s/bin/endless-go hook claude"\n' % broken
    )

    proc = _sweep(repo, home)
    assert proc.returncode != 0
    assert f"FAILED on {broken}" in proc.stderr, proc.stderr
    assert "stopped after" in proc.stderr, proc.stderr
    assert "re-run" in proc.stderr, proc.stderr

    # It did not claim success, and the worktree it choked on is untouched.
    assert "worktree(s) visited" not in proc.stdout
    assert _is_pinned(broken), "the broken worktree was rewritten anyway"
    swept = [wt for wt in worktrees if not _is_pinned(wt)]
    assert len(swept) < len(worktrees)


def test_it_refuses_to_run_from_a_worktree(project):
    """It sweeps every worktree, so running it from inside one overreaches."""
    repo, worktrees, home = project
    proc = _sweep(repo, home, cwd=worktrees[0])
    assert proc.returncode != 0
    assert "refusing to run from a worktree" in proc.stderr, proc.stderr
    assert all(_is_pinned(wt) for wt in worktrees), "it swept despite refusing"


def test_an_abandoned_directory_is_neither_swept_nor_a_failure(project):
    """Enumeration comes from `git worktree list`, not a directory glob.

    A leftover directory under .endless/worktrees/ is not a worktree, has no
    session running in it, and must not stop the sweep — `git rev-parse` inside
    it would report the enclosing repo and the recipe's main-checkout refusal
    would fire.
    """
    repo, worktrees, home = project
    stray = repo / ".endless" / "worktrees" / "e-999"
    stray.mkdir()
    _pin(stray)

    proc = _sweep(repo, home)
    assert proc.returncode == 0, proc.stderr
    assert "3 worktree(s) visited" in proc.stdout, proc.stdout
    assert _is_pinned(stray), "the stray directory was swept"
