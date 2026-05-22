"""Tests for E-1342: pre-rebase orphan auto-amend commit drop in land.

Covers _drop_orphan_amendable_commits, which strips contiguous ledger
orphan commits at the base of a task branch before the regular
`git rebase main` in land_worktree(). Uses real git repos because the
helper shells out to git log + rebase.
"""

import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    AMENDABLE_COMMIT_SUBJECTS,
    _drop_orphan_amendable_commits,
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


def _log_subjects(repo: Path, range_spec: str) -> list[str]:
    out = _run(["git", "log", "--reverse", "--format=%s", range_spec], repo).stdout
    return [s for s in out.splitlines() if s.strip()]


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

def test_amendable_subjects_includes_ledger():
    assert LEDGER_SUBJECT in AMENDABLE_COMMIT_SUBJECTS


# ---------------------------------------------------------------------------
# Helper behavior
# ---------------------------------------------------------------------------

def test_no_orphans_returns_noop(repo_with_worktree):
    """Task branch with only user commits: helper is a no-op."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})

    pre_sha = _head_sha(wt)
    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 0
    assert subj is None
    assert _head_sha(wt) == pre_sha  # no rebase happened


def test_empty_branch_returns_noop(repo_with_worktree):
    """Task branch == main (no commits ahead): helper is a no-op."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])

    pre_sha = _head_sha(wt)
    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 0
    assert subj is None
    assert _head_sha(wt) == pre_sha


def test_single_ledger_orphan_at_base_dropped(repo_with_worktree):
    """Single ledger orphan at base + user commit on top:
    drop the orphan, keep the user commit."""
    main = repo_with_worktree["main"]
    # Add a ledger commit on main (simulating an auto-record on main).
    _commit(main, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})

    # Fork the task branch from this ledger commit.
    wt = _create_task_branch(main, repo_with_worktree["tmp"])

    # Add the user's real commit on top.
    _commit(wt, "user work", {"hello.txt": "hello\n"})

    # On main, amend the ledger commit so its SHA changes (canAmend flow).
    _amend_with_extra_line(main, ".endless/db-ledger/x.jsonl", '{"a":2}\n')

    # Now main's ledger SHA differs from the wt branch's base.
    # Before fix: `main..HEAD` shows [ledger-orphan, user-work].
    pre_subjects = _log_subjects(wt, "main..HEAD")
    assert pre_subjects == [LEDGER_SUBJECT, "user work"]

    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 1
    assert subj == LEDGER_SUBJECT

    # After: only the user commit remains ahead of main.
    post_subjects = _log_subjects(wt, "main..HEAD")
    assert post_subjects == ["user work"]


def test_two_contiguous_ledger_orphans_dropped(repo_with_worktree):
    """Two contiguous orphans at base: drop both, keep the user commit.
    Constructs the orphan layout directly on the task branch rather than
    via canAmend simulation — the helper's contract is about branch shape,
    not about how the shape arose."""
    main = repo_with_worktree["main"]
    wt = _create_task_branch(main, repo_with_worktree["tmp"])

    # Put two amendable-subject commits at the branch base, then a user
    # commit on top. None of these are reachable from main.
    _commit(wt, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})
    _commit(wt, LEDGER_SUBJECT, {".endless/db-ledger/y.jsonl": '{"b":2}\n'})
    _commit(wt, "user work", {"hello.txt": "hello\n"})

    pre_subjects = _log_subjects(wt, "main..HEAD")
    assert pre_subjects == [LEDGER_SUBJECT, LEDGER_SUBJECT, "user work"]

    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 2
    assert subj == LEDGER_SUBJECT  # oldest first

    post_subjects = _log_subjects(wt, "main..HEAD")
    assert post_subjects == ["user work"]


def test_mid_branch_amendable_subject_is_preserved(repo_with_worktree):
    """Orphan at base + user commit + a commit whose subject happens to
    match an amendable subject: only the BASE orphan is dropped. The
    mid-branch amendable-subject commit is preserved (it's not at the
    contiguous-from-base position)."""
    main = repo_with_worktree["main"]
    _commit(main, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})

    wt = _create_task_branch(main, repo_with_worktree["tmp"])
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    # An "amendable subject" commit mid-branch (e.g., a stray pre-E-1309
    # contamination, or — implausibly — a user commit that happens to be
    # subject-titled like an auto-commit). Helper must NOT drop it.
    _commit(wt, LEDGER_SUBJECT, {"ledgerish.txt": "looks like an auto-commit\n"})

    _amend_with_extra_line(main, ".endless/db-ledger/x.jsonl", '{"a":2}\n')

    pre_subjects = _log_subjects(wt, "main..HEAD")
    assert pre_subjects == [LEDGER_SUBJECT, "user work", LEDGER_SUBJECT]

    count, subj = _drop_orphan_amendable_commits(wt, "main")
    assert count == 1
    assert subj == LEDGER_SUBJECT

    post_subjects = _log_subjects(wt, "main..HEAD")
    assert post_subjects == ["user work", LEDGER_SUBJECT]


def test_drop_keeps_head_attached_to_branch(repo_with_worktree):
    """E-1355: after orphan-drop, HEAD must remain attached to the task
    branch and the branch ref must point at the new HEAD. Earlier
    behavior used `git rebase --onto X Y HEAD` which detaches HEAD,
    stranding the branch ref at the pre-rebase tip — Step 5's ff-merge
    in land then targets an ancestor of main and fails permanently
    with 'diverging branches' (no retry count helps)."""
    main = repo_with_worktree["main"]
    _commit(main, LEDGER_SUBJECT, {".endless/db-ledger/x.jsonl": '{"a":1}\n'})

    wt = _create_task_branch(main, repo_with_worktree["tmp"], name="task/x")
    _commit(wt, "user work", {"hello.txt": "hello\n"})
    _amend_with_extra_line(main, ".endless/db-ledger/x.jsonl", '{"a":2}\n')

    pre_branch_ref = _run(["git", "rev-parse", "task/x"], wt).stdout.strip()
    pre_head = _head_sha(wt)
    assert pre_branch_ref == pre_head, "sanity: HEAD on branch before drop"

    count, _ = _drop_orphan_amendable_commits(wt, "main")
    assert count == 1

    # HEAD must still be a symbolic ref to the task branch (not detached).
    symref = _run(
        ["git", "symbolic-ref", "HEAD"], wt, check=True
    ).stdout.strip()
    assert symref == "refs/heads/task/x", (
        f"HEAD became detached after orphan-drop: {symref!r}"
    )

    # And the branch ref must point at the post-drop HEAD, not the
    # pre-drop tip.
    post_head = _head_sha(wt)
    post_branch_ref = _run(["git", "rev-parse", "task/x"], wt).stdout.strip()
    assert post_branch_ref == post_head, (
        f"branch ref {post_branch_ref} not aligned with HEAD {post_head} "
        f"after orphan-drop (pre-drop tip was {pre_head})"
    )
    assert post_branch_ref != pre_head, (
        "branch ref unchanged after drop; orphan-drop did not advance it"
    )
