"""E-2461: `worktree land` refuses a task with an unfinished blocker.

Claim, spawn and prime already refuse a task whose blocker is not terminal
(E-2296, tests/test_blocks_gate.py); land ran no such check, so E-2405 landed
while E-2412 still blocked it. Land now runs the same `_require_unblocked`, with
the same release rule, wording and remedy, before anything moves.

Against the test database and a real git repository: the task, its blockers and
their links are real rows, and the branch, main and the merge-base check are
real git. What is stubbed is the worktree registry lookup (`_enriched_list`,
`_branch_for_task`, `_project_root`) and the first step past the gate, which
the tests replace with a sentinel so a land the gate lets through stops there.
"""

import subprocess

import click
import pytest

from endless import db, task_cmd, worktree_cmd

TASK = 5161


class _PastTheGate(Exception):
    """Raised by the first step after the blocker check."""


def _git(cwd, *args) -> str:
    return subprocess.run(["git", "-C", str(cwd), *args], check=True,
                          capture_output=True, text=True).stdout.strip()


def _status(item_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (item_id,))[0]["status"]


def _set_status(item_id: int, status: str) -> None:
    """A raw write: the setup needs a task sitting IN a status."""
    db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, item_id))


@pytest.fixture
def repo(seeded_project_at_cwd, monkeypatch):
    """TASK in `unlanded`, its branch in a worktree one commit ahead of main,
    and land pointed at that worktree."""
    root = seeded_project_at_cwd
    db.execute(
        "INSERT INTO tasks (id, project_id, title, description, status, type_id, phase) "
        "VALUES (?, (SELECT id FROM projects WHERE name = 'test'), 'Land the work', "
        "'d', 'unlanded', (SELECT id FROM task_types WHERE slug = 'todo'), 'now')",
        (TASK,),
    )
    wt = root / ".endless" / "worktrees" / f"e-{TASK}"
    _git(root, "worktree", "add", "-q", "-b", f"task/{TASK}", str(wt))
    _git(wt, "config", "user.email", "t@example.com")
    _git(wt, "config", "user.name", "T")
    (wt / "code.py").write_text("x = 1\n")
    _git(wt, "add", "-A")
    _git(wt, "commit", "-q", "-m", "the work")

    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: root)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda r: [])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task",
        lambda rows, canonical: {"branch": f"task/{TASK}", "path": str(wt),
                                 "companion": {"base_branch": "main"}},
    )

    def past(*a, **k):
        raise _PastTheGate

    # The dry run's first step after the gate, and the real land's.
    monkeypatch.setattr(worktree_cmd, "_refuse_if_land_gated", past)
    monkeypatch.setattr(worktree_cmd, "_resolve_land_endless_go", past)
    return {"root": root, "wt": wt}


def _blocker(status: str = "submitted", title: str = "Fix each fault") -> int:
    blocker = task_cmd.add_item(title=title, description="d", plan="# Plan")
    task_cmd.link_tasks(blocker, TASK, "blocks")
    if status != "submitted":
        _set_status(blocker, status)
    return blocker


def _refusal(dry_run: bool = False) -> str:
    with pytest.raises(click.ClickException) as exc:
        worktree_cmd.land_worktree(f"E-{TASK}", dry_run=dry_run)
    assert not isinstance(exc.value, _PastTheGate)
    return str(exc.value)


def _passes(dry_run: bool = True) -> None:
    with pytest.raises(_PastTheGate):
        worktree_cmd.land_worktree(f"E-{TASK}", dry_run=dry_run)


# ---------------------------------------------------------------------------
# P1, P2: refuses before anything moves, naming each blocker and its unblock
# ---------------------------------------------------------------------------

def test_land_refuses_and_changes_nothing(repo):
    blocker = _blocker()
    main_before = _git(repo["root"], "rev-parse", "main")
    branch_before = _git(repo["root"], "rev-parse", f"task/{TASK}")

    msg = _refusal()

    assert f"Cannot land E-{TASK}" in msg
    assert f"E-{blocker}" in msg
    assert _git(repo["root"], "rev-parse", "main") == main_before
    assert _git(repo["root"], "rev-parse", f"task/{TASK}") == branch_before
    assert _status(TASK) == "unlanded"


def test_the_refusal_names_each_blocker_and_its_unblock(repo):
    first = _blocker(title="Fix each fault")
    second = _blocker("underway", title="Fix each error")

    msg = _refusal()

    assert f"E-{first}  submitted  Fix each fault" in msg
    assert f"E-{second}  underway  Fix each error" in msg
    assert f"endless task unblock E-{TASK} --by E-{first}" in msg
    assert f"endless task unblock E-{TASK} --by E-{second}" in msg


# ---------------------------------------------------------------------------
# P3: only a terminal blocker releases the land
# ---------------------------------------------------------------------------

def test_a_landed_blocker_still_refuses(repo):
    blocker = _blocker("landed")
    assert f"E-{blocker}  landed  Fix each fault" in _refusal()


@pytest.mark.parametrize("status", ["assumed", "declined"])
def test_a_terminal_blocker_releases_the_land(repo, status):
    _blocker(status)
    _passes()


def test_an_unblocked_task_lands(repo):
    blocker = _blocker()
    task_cmd.unlink_tasks(blocker, TASK, "blocks")
    _passes(dry_run=False)


# ---------------------------------------------------------------------------
# P4: a blocker in another project counts
# ---------------------------------------------------------------------------

def test_a_blocker_in_another_project_refuses(repo, tmp_path):
    from conftest import init_git_repo

    other = init_git_repo(tmp_path / "other")
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('other', ?, 'active', datetime('now'), datetime('now'))",
        (str(other),),
    )
    blocker = task_cmd.add_item(title="Fix the library", description="d",
                                plan="# Plan", project_name="other")
    task_cmd.link_tasks(blocker, TASK, "blocks")

    assert f"E-{blocker}  submitted  Fix the library" in _refusal()


# ---------------------------------------------------------------------------
# P5: --dry-run refuses the same way
# ---------------------------------------------------------------------------

def test_dry_run_refuses_the_same_way(repo):
    blocker = _blocker()
    assert _refusal(dry_run=True) == _refusal(dry_run=False)
    assert f"endless task unblock E-{TASK} --by E-{blocker}" in _refusal(dry_run=True)


# ---------------------------------------------------------------------------
# P6, P7: a land that already happened is not refused
# ---------------------------------------------------------------------------

def test_a_rerun_whose_branch_is_already_merged_is_not_refused(repo):
    """The first run merged, then failed later (a build, a migration): the
    code is already in main, so refusing protects nothing."""
    _blocker()
    _git(repo["wt"], "rebase", "-q", "main")
    _git(repo["root"], "merge", "-q", "--ff-only", f"task/{TASK}")
    _passes(dry_run=False)


def test_a_branch_merely_behind_main_is_still_refused(repo):
    """Main moving on does not make the branch merged."""
    blocker = _blocker()
    _git(repo["root"], "commit", "-q", "--allow-empty", "-m", "main moves on")
    assert f"E-{blocker}" in _refusal()


def test_record_only_is_not_refused(repo, monkeypatch):
    _blocker()
    recorded = []
    monkeypatch.setattr(worktree_cmd, "_record_only_landing",
                        lambda *a, **k: recorded.append(a))
    worktree_cmd.land_worktree(f"E-{TASK}", dry_run=False, record_only=True,
                               sha="abc1234")
    assert recorded
