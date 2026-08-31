"""`endless worktree sync` — the drift sweep's per-worktree decision (E-2090).

A worktree branched before a change landed does not have that change until
someone rebases it. When the change is a guard that every verify suite depends
on, "until someone rebases it" is the whole exposure: the fix reaches main and
reaches no working checkout.

`_sync_state` is where the sweep decides what to do with one worktree, and every
skip it can return protects something — a session's uncommitted work, or the
branch the sweeping process is itself standing on. These test that directly
against real git, because the failure mode is rewriting a branch somebody is
working on, which no amount of mocking would catch.
"""

import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import _sync_state


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def repo_and_worktree(tmp_path):
    """A main checkout one commit AHEAD of a linked worktree — the drift shape."""
    main = tmp_path / "main"
    main.mkdir()
    _run(["git", "init", "-q", "-b", "main"], main)
    _run(["git", "config", "user.email", "t@t.t"], main)
    _run(["git", "config", "user.name", "t"], main)
    (main / "shared.txt").write_text("v1\n")
    (main / ".endless").mkdir()
    (main / ".endless" / "verbs.jsonl").write_text("")
    _run(["git", "add", "-A"], main)
    _run(["git", "commit", "-q", "-m", "init"], main)

    wt = tmp_path / "wt"
    _run(["git", "worktree", "add", "-q", "-b", "feat", str(wt)], main)

    # main moves on; the worktree does not.
    (main / "shared.txt").write_text("v2\n")
    _run(["git", "commit", "-qam", "the change the worktree does not have"], main)
    return main, wt


def test_a_behind_worktree_is_a_rebase_candidate(repo_and_worktree):
    _, wt = repo_and_worktree
    disposition, reason = _sync_state(wt, "main", here=None)
    assert disposition == "rebase"
    assert "behind main" in reason


def test_a_current_worktree_is_left_alone(repo_and_worktree):
    main, wt = repo_and_worktree
    _run(["git", "rebase", "-q", "main"], wt)
    disposition, reason = _sync_state(wt, "main", here=None)
    assert disposition == "skip"
    assert reason == "already on main"


def test_uncommitted_user_work_is_never_rebased_over(repo_and_worktree):
    """The skip that matters most: those changes are a live session's work."""
    _, wt = repo_and_worktree
    (wt / "in-flight.py").write_text("half a thought\n")
    disposition, reason = _sync_state(wt, "main", here=None)
    assert disposition == "skip"
    assert "in-flight.py" in reason, "the skip must name the file, not cite a rule"


def test_uncommitted_endless_managed_files_are_skipped_and_named_as_such(repo_and_worktree):
    """Still skipped — but calling a ledger write 'a session mid-flight' is false."""
    _, wt = repo_and_worktree
    (wt / ".endless" / "verbs.jsonl").write_text('{"v": 1}\n')
    disposition, reason = _sync_state(wt, "main", here=None)
    assert disposition == "skip"
    assert "endless-managed" in reason


def test_the_worktree_you_are_standing_in_is_skipped(repo_and_worktree):
    """Rebasing it would rewrite the branch under the process doing the rewrite."""
    _, wt = repo_and_worktree
    disposition, reason = _sync_state(wt, "main", here=wt)
    assert disposition == "skip"
    assert reason == "you are in it"


def test_an_unanswerable_comparison_is_an_error_not_a_rebase(repo_and_worktree):
    """A base branch that does not exist must never read as 'behind'."""
    _, wt = repo_and_worktree
    disposition, _reason = _sync_state(wt, "no-such-branch", here=None)
    assert disposition == "error"


def test_rebasing_actually_delivers_the_change(repo_and_worktree):
    """The point of the sweep, end to end: the worktree gains what main has."""
    _, wt = repo_and_worktree
    assert (wt / "shared.txt").read_text() == "v1\n"
    assert _sync_state(wt, "main", here=None)[0] == "rebase"
    _run(["git", "rebase", "-q", "main"], wt)
    assert (wt / "shared.txt").read_text() == "v2\n"
    assert _sync_state(wt, "main", here=None) == ("skip", "already on main")
