"""Tests for E-1736: land gate refusing branch-side DB ledger commits.

Covers _ledger_touching_commits, which enumerates the commits in
base..HEAD that modify a file under .endless/db-ledger/. Land runs it
AFTER Step 3.7's orphan-drop and refuses when it returns anything: a
branch-authored ledger commit would be rebased into main and corrupt the
shared database history (the ED-1525 routing policy — ledger entries are
recorded on the main checkout only). Uses real git repos because the
helper shells out to git log.
"""

import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    DB_LEDGER_DIR,
    _drop_orphan_amendable_commits,
    _ledger_touching_commits,
)


LEDGER_SUBJECT = "Endless: record ledger entry"


def _run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True
    )


def _commit(repo: Path, msg: str, files: dict[str, str]):
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)


def _head_sha(repo: Path) -> str:
    return _run(["git", "rev-parse", "HEAD"], repo).stdout.strip()


def _amend_with_extra_line(repo: Path, file_rel: str, extra: str):
    """Simulate canAmend rewriting the ledger commit: append to a file
    and `git commit --amend` so the HEAD commit's SHA changes but its
    subject stays the same."""
    p = repo / file_rel
    p.write_text(p.read_text() + extra)
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "--amend", "--no-edit"], repo)


@pytest.fixture
def repo_with_worktree(tmp_path):
    """Create a main repo plus a task worktree forked from main's HEAD."""
    main = tmp_path / "main"
    main.mkdir()
    _run(["git", "init", "-q", "-b", "main"], main)
    _run(["git", "config", "user.email", "t@t.t"], main)
    _run(["git", "config", "user.name", "t"], main)
    _commit(main, "init", {"README.md": "init\n"})
    return {"main": main, "tmp": tmp_path}


def _create_task_branch(main: Path, tmp: Path, name: str = "task/x") -> Path:
    wt = tmp / "wt"
    _run(["git", "worktree", "add", "-q", str(wt), "-b", name, "main"], main)
    return wt


# ---------------------------------------------------------------------------
# Constants sanity
# ---------------------------------------------------------------------------

def test_ledger_dir_pathspec():
    assert DB_LEDGER_DIR == ".endless/db-ledger"


# ---------------------------------------------------------------------------
# Helper behavior
# ---------------------------------------------------------------------------

def test_clean_branch_no_offenders(repo_with_worktree):
    """Task branch with only user commits: helper returns []."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})

    assert _ledger_touching_commits(wt, "main") == []


def test_empty_branch_no_offenders(repo_with_worktree):
    """Task branch == main (no commits ahead): helper returns []."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])

    assert _ledger_touching_commits(wt, "main") == []


def test_standalone_ledger_commit_is_offender(repo_with_worktree):
    """A branch-side commit touching the ledger dir is reported."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    _commit(wt, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})

    offenders = _ledger_touching_commits(wt, "main")
    assert len(offenders) == 1
    sha, subject = offenders[0]
    assert subject == LEDGER_SUBJECT
    assert sha == _head_sha(wt)


def test_multiple_ledger_commits_all_reported(repo_with_worktree):
    """Every ledger-touching commit in base..HEAD is reported, oldest
    first (--reverse)."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    _commit(wt, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})
    _commit(wt, "more user work", {"world.txt": "world\n"})
    _commit(wt, "sneaky ledger edit", {".endless/db-ledger/y.jsonl": '{"b":2}\n'})

    offenders = _ledger_touching_commits(wt, "main")
    assert [subj for _, subj in offenders] == [
        LEDGER_SUBJECT,
        "sneaky ledger edit",
    ]


def test_non_ledger_endless_files_not_offenders(repo_with_worktree):
    """A commit touching other .endless/ files (e.g. verbs.jsonl) but not
    the ledger dir is NOT reported — the gate is scoped to db-ledger."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "verbs churn", {".endless/verbs.jsonl": '{"v":1}\n'})

    assert _ledger_touching_commits(wt, "main") == []


# ---------------------------------------------------------------------------
# Interaction with Step 3.7's orphan-drop (the critical regression)
# ---------------------------------------------------------------------------

def test_orphan_no_false_positive_after_step_3_7(repo_with_worktree):
    """The E-1342 orphan scenario must NOT trip the gate. A branch forks at
    an old ledger tip; main later amends that tip, orphaning the branch's
    fork-point copy so it appears in main..HEAD touching the ledger. Land
    runs Step 3.7 (orphan-drop) BEFORE the gate — after the drop, the helper
    must report no offenders."""
    main = repo_with_worktree["main"]
    # Ledger commit on main, then fork the branch off it.
    _commit(main, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    # main amends the ledger commit → branch's base copy is now an orphan.
    _amend_with_extra_line(main, ".endless/db-ledger/x.jsonl", '{"a":2}\n')

    # Before Step 3.7 the orphan is a ledger-touching commit in main..HEAD.
    assert len(_ledger_touching_commits(wt, "main")) == 1

    # Land drops it in Step 3.7 first...
    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 1
    assert subj == LEDGER_SUBJECT

    # ...so the gate sees no offenders — no false positive on a normal land.
    assert _ledger_touching_commits(wt, "main") == []


def test_mid_branch_ledger_commit_survives_step_3_7_and_is_caught(
    repo_with_worktree,
):
    """A ledger commit that is NOT a contiguous base orphan survives Step 3.7
    (which breaks at the first non-amendable commit) and must be caught by
    the gate. User commit at base, ledger commit on top."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    _commit(wt, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})

    # Step 3.7 drops nothing: the base commit is the user's, not a ledger orphan.
    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 0
    assert subj is None

    # The gate catches the mid-branch ledger commit.
    offenders = _ledger_touching_commits(wt, "main")
    assert len(offenders) == 1
    assert offenders[0][1] == LEDGER_SUBJECT
