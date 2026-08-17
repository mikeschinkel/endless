"""E-1947: `endless worktree drop` must refuse to remove a worktree that is
still in use, adopting BOTH of the reaper's guards.

Drop previously checked only foreign state and uncommitted changes, so it
happily removed the directory a live process was sitting in — orphaning that
session's cwd, recovered only by mkdir'ing the directory back.

The two guards, and what each catches on its own:
  - a non-ended `sessions` row whose task_id is this task — a bound
    Claude session whose cwd stepped out of the worktree temporarily, which the
    lsof probe cannot see;
  - a live process holding cwd inside the directory — any process, Claude or
    not, which the sessions table cannot see.

Both refusals are behind --force, as drop's existing refusals are.

These tests exec the real `endless-go worktree in-use` verb: conftest prepends
this worktree's bin/ to PATH and points XDG_CONFIG_HOME / RESOLVED_CONFIG_DIR at
the same isolated DB the Python `db` module seeds, so the Go side reads exactly
the rows seeded here.
"""

import shutil
import subprocess
import time
from pathlib import Path

import click
import pytest

from endless import db
from endless.worktree_cmd import drop_worktree

TASK_ID = 9999


@pytest.fixture
def dropable_worktree(seeded_project_at_cwd):
    """A clean, endless-managed worktree at .endless/worktrees/e-9999.

    `.endless/` is gitignored so the companion file the classifier needs does
    not itself show up as an uncommitted change (which would trip drop's
    pre-existing guard and mask the one under test).
    """
    proj = seeded_project_at_cwd

    def _git(*args, cwd=proj):
        subprocess.run(["git", *args], cwd=str(cwd), check=True,
                       capture_output=True)

    (proj / ".gitignore").write_text(".endless/\n")
    _git("add", ".gitignore")
    _git("commit", "-q", "-m", "ignore .endless")

    wt = proj / ".endless" / "worktrees" / f"e-{TASK_ID}"
    _git("worktree", "add", "-q", "-b", f"task/{TASK_ID}", str(wt))
    companion = wt / ".endless"
    companion.mkdir(parents=True, exist_ok=True)
    (companion / "worktree.json").write_text('{"project": "test"}')

    yield wt

    for p in (wt,):
        if p.exists():
            subprocess.run(["git", "worktree", "remove", "--force", str(p)],
                           cwd=str(proj), capture_output=True)


@pytest.fixture
def live_process_in(dropable_worktree):
    """A real process whose cwd is inside the worktree, reaped on teardown."""
    proc = subprocess.Popen(["sleep", "120"], cwd=str(dropable_worktree))
    # lsof reads /proc-equivalent state; give the child a moment to be exec'd
    # so its cwd is observable.
    time.sleep(0.3)
    yield proc
    proc.terminate()
    proc.wait(timeout=10)


def _seed_session(state: str) -> None:
    """A sessions row bound to TASK_ID in the given lifecycle state."""
    pid = db.query("SELECT id FROM projects WHERE name = 'test'")[0]["id"]
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, type_id, phase, "
        "created_at) VALUES (?, ?, 'probe', 'underway', 1, 'now', "
        "datetime('now'))",
        (TASK_ID, pid),
    )
    db.execute(
        "INSERT INTO sessions (session_id, project_id, platform, state, "
        "task_id, started_at, last_activity) "
        "VALUES ('uuid-probe', ?, 'claude', ?, ?, datetime('now'), "
        "datetime('now'))",
        (pid, state, TASK_ID),
    )


@pytest.fixture(autouse=True)
def _require_go_binary():
    if shutil.which("endless-go") is None:
        pytest.skip("endless-go not on PATH; run `just build`")


# ── guard (b): a live process holds cwd inside the worktree ────────────────

def test_drop_refuses_when_a_process_holds_cwd_inside(
    dropable_worktree, live_process_in
):
    with pytest.raises(click.ClickException) as exc:
        drop_worktree(f"e-{TASK_ID}", force=False)
    assert "in use" in exc.value.message.lower()
    assert dropable_worktree.exists()


def test_force_overrides_the_live_process_guard(
    dropable_worktree, live_process_in
):
    drop_worktree(f"e-{TASK_ID}", force=True)
    assert not dropable_worktree.exists()


# ── guard (a): a non-ended session row has this task active ───────────────

def test_drop_refuses_when_a_non_ended_session_has_the_task_active(
    dropable_worktree
):
    _seed_session("working")
    with pytest.raises(click.ClickException) as exc:
        drop_worktree(f"e-{TASK_ID}", force=False)
    assert "in use" in exc.value.message.lower()
    assert dropable_worktree.exists()


def test_ended_session_does_not_block_drop(dropable_worktree):
    _seed_session("ended")
    drop_worktree(f"e-{TASK_ID}", force=False)
    assert not dropable_worktree.exists()


# ── the idle worktree still drops ─────────────────────────────────────────

def test_idle_worktree_still_drops(dropable_worktree):
    drop_worktree(f"e-{TASK_ID}", force=False)
    assert not dropable_worktree.exists()
