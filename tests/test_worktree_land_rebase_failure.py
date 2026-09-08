"""Tests for E-2122: land reports what git actually said when a rebase fails.

`endless worktree land` treated EVERY non-zero exit from `git rebase` as a
content conflict. `git rebase` also exits non-zero when it refuses to start —
a dirty worktree, a rebase already in progress, an operational failure — and
none of those produce unmerged paths or are fixed by a conflict's recoveries.
The result asserted a conflict that never happened, listed no conflicting files,
and offered candidate recoveries for a cause it had never established. Git's
stderr, which names the real reason, was captured in CalledProcessError and
discarded.

Uses real git repos because the code under test reads live rebase state
(`git diff --diff-filter=U`, REBASE_HEAD, the rebase-merge/rebase-apply dirs).
"""

import subprocess
from pathlib import Path

from endless.worktree_cmd import (
    _git_said,
    _rebase_failure_message,
    _rebase_in_progress,
)


def _run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True
    )


def _init_repo(path: Path) -> Path:
    path.mkdir(parents=True, exist_ok=True)
    _run(["git", "init", "-q", "-b", "main"], path)
    _run(["git", "config", "user.email", "t@t.t"], path)
    _run(["git", "config", "user.name", "t"], path)
    (path / "f.txt").write_text("base\n")
    _run(["git", "add", "-A"], path)
    _run(["git", "commit", "-q", "-m", "base"], path)
    return path


def _branch_and_advance_main(repo: Path, *, branch_file: str, main_file: str) -> None:
    """A branch with one commit, and a main that has moved on. When the two
    files differ the rebase replays cleanly; when they are the same it conflicts."""
    _run(["git", "checkout", "-q", "-b", "feature"], repo)
    (repo / branch_file).write_text("branch edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "my real work"], repo)

    _run(["git", "checkout", "-q", "main"], repo)
    (repo / main_file).write_text("main edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "main moved on"], repo)
    _run(["git", "checkout", "-q", "feature"], repo)


def _attempt_rebase(repo: Path):
    """Exactly what both land call sites do: note whether a rebase was already
    running, run the rebase, and on failure build the report from git's stderr.
    Returns (message, pre_existing) or (None, pre_existing) when it succeeded."""
    pre_existing = _rebase_in_progress(repo)
    try:
        subprocess.run(
            ["git", "rebase", "main"],
            cwd=str(repo), check=True, capture_output=True, text=True,
        )
        return None, pre_existing
    except subprocess.CalledProcessError as e:
        msg = _rebase_failure_message(
            repo, "main",
            phase="rebasing your branch onto main",
            stderr=e.stderr, pre_existing=pre_existing,
        )
        return msg, pre_existing


# ─── the non-conflict failure: git's words, no invented conflict ───────────────

def test_dirty_worktree_is_reported_as_a_failure_not_a_conflict(tmp_path):
    """The observed shape: rebase refuses to start, nothing conflicts.

    Reachable at land Step 3.7, whose rebase runs BEFORE Step 3.8's
    dirty-worktree guard.
    """
    repo = _init_repo(tmp_path / "repo")
    _branch_and_advance_main(repo, branch_file="mine.txt", main_file="other.txt")
    (repo / "mine.txt").write_text("uncommitted edit\n")

    msg, _ = _attempt_rebase(repo)
    assert msg is not None, "expected the rebase to refuse to start"

    # It does not claim a conflict, and does not print the empty file list that
    # was the tell that no conflict had been established.
    assert "rebase conflict" not in msg
    assert "(none reported)" not in msg
    assert "Conflicting files" not in msg
    assert "rebase failed while rebasing your branch onto main" in msg
    assert "NOT a content conflict" in msg

    # Git's own reason survives — the whole point of the task.
    assert "unstaged changes" in msg
    assert "git said:" in msg

    # No recovery candidates: the cause is stated, so there is nothing to judge.
    assert "the wrong recovery can duplicate or lose work" not in msg
    assert "git rebase --continue" not in msg
    assert "reset --hard main" not in msg


def test_failure_message_does_not_name_a_failing_commit(tmp_path):
    """No rebase is in progress, so REBASE_HEAD must not be consulted."""
    repo = _init_repo(tmp_path / "repo")
    _branch_and_advance_main(repo, branch_file="mine.txt", main_file="other.txt")
    (repo / "mine.txt").write_text("uncommitted edit\n")

    msg, _ = _attempt_rebase(repo)
    assert "failed to replay" not in msg


# ─── a rebase land did not start: report it, and leave it alone ───────────────

def test_pre_existing_rebase_is_named_and_not_blamed_on_the_branch(tmp_path):
    """A rebase already stopped in the worktree is a different failure.

    Its unmerged paths and REBASE_HEAD belong to that operation, and reading
    them reported another branch's commit as the cause of this failure.
    """
    repo = _init_repo(tmp_path / "repo")
    _run(["git", "checkout", "-q", "-b", "other"], repo)
    (repo / "f.txt").write_text("other branch edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "UNRELATED COMMIT from another operation"], repo)

    _run(["git", "checkout", "-q", "main"], repo)
    (repo / "f.txt").write_text("main edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "main moved on"], repo)

    _run(["git", "checkout", "-q", "other"], repo)
    _run(["git", "rebase", "main"], repo, check=False)   # conflicts, left running
    assert _rebase_in_progress(repo), "fixture should leave a rebase in progress"

    msg, pre_existing = _attempt_rebase(repo)
    assert pre_existing is True
    assert "already in progress" in msg
    assert "land did not begin one" in msg

    # The other operation's state is never presented as this failure's cause.
    assert "UNRELATED COMMIT" not in msg
    assert "failed to replay" not in msg
    assert "rebase conflict" not in msg


def test_pre_existing_rebase_survives_the_report(tmp_path):
    """Land must not abort a rebase it did not start — that discarded the
    user's in-progress conflict resolution."""
    repo = _init_repo(tmp_path / "repo")
    _run(["git", "checkout", "-q", "-b", "other"], repo)
    (repo / "f.txt").write_text("other branch edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "work being resolved"], repo)

    _run(["git", "checkout", "-q", "main"], repo)
    (repo / "f.txt").write_text("main edit\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "main moved on"], repo)

    _run(["git", "checkout", "-q", "other"], repo)
    _run(["git", "rebase", "main"], repo, check=False)

    msg, pre_existing = _attempt_rebase(repo)
    assert pre_existing is True
    # The caller aborts only when it started the rebase itself.
    assert _rebase_in_progress(repo), "land must leave the other rebase running"
    assert "left exactly as it was" in msg

    _run(["git", "rebase", "--abort"], repo, check=False)


# ─── a genuine conflict still reports as a conflict ────────────────────────────

def test_real_conflict_still_routes_to_the_conflict_report(tmp_path):
    """The fix must not suppress real conflicts — same file on both sides."""
    repo = _init_repo(tmp_path / "repo")
    _branch_and_advance_main(repo, branch_file="f.txt", main_file="f.txt")

    msg, pre_existing = _attempt_rebase(repo)
    assert pre_existing is False
    assert "rebase conflict while rebasing your branch onto main" in msg
    assert "f.txt" in msg
    assert "(none reported)" not in msg
    # The failing commit is named, because a rebase really is in progress.
    assert "failed to replay" in msg and "my real work" in msg
    # And git's words are carried as context.
    assert "git said:" in msg
    # E-1957 removed the two candidate recoveries this used to assert: both
    # restored the branch's side of the hunk, which reintroduces anything the
    # base branch has deleted. The conflict report still routes HERE — which is
    # what this test is about — it just hands the classification off instead of
    # guessing at it.
    assert "endless worktree diagnose" in msg
    assert "the wrong recovery can duplicate or lose work" not in msg
    assert "land-delta.patch" not in msg
    # `git rebase --continue` still appears — inside git's quoted hints, which
    # E-2122 carries verbatim on purpose. What must not appear is endless
    # RECOMMENDING it. The report says so in as many words.
    assert "Do not follow it yet" in msg

    _run(["git", "rebase", "--abort"], repo, check=False)


# ─── the helpers ──────────────────────────────────────────────────────────────

def test_rebase_in_progress_is_false_on_a_quiet_repo(tmp_path):
    repo = _init_repo(tmp_path / "repo")
    assert _rebase_in_progress(repo) is False


def test_rebase_in_progress_is_false_after_a_clean_rebase(tmp_path):
    repo = _init_repo(tmp_path / "repo")
    _branch_and_advance_main(repo, branch_file="mine.txt", main_file="other.txt")
    _run(["git", "rebase", "main"], repo)
    assert _rebase_in_progress(repo) is False


def test_git_said_indents_every_line(tmp_path):
    out = _git_said("error: cannot rebase\nerror: please commit\n")
    assert out == "  error: cannot rebase\n  error: please commit"


def test_git_said_reports_silence_rather_than_an_empty_block(tmp_path):
    for empty in (None, "", "   \n  \n"):
        assert _git_said(empty) == "  (git printed nothing on stderr)"
