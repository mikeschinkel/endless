"""Tests for _rebase_conflict_message: accurate rebase-conflict reporting in land.

`endless worktree land` rebases a task branch in two places (Step 3.7 orphan
replay + Step 4 main rebase); both previously misattributed a content conflict —
sending the user to the wrong step with a recovery that could not work. The
shared helper reports the facts confidently (which step, which files, which
commit) and offers recoveries as candidates to judge between.

Uses real git repos because the helper reads live rebase-conflict state
(`git diff --diff-filter=U`, REBASE_HEAD). Each test leaves a rebase in progress,
calls the helper, then aborts.
"""

import re
import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    AUTO_COMMIT_GLOBS,
    _rebase_conflict_message,
)


def _run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True
    )


def _init_repo(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)
    _run(["git", "init", "-q", "-b", "main"], path)
    _run(["git", "config", "user.email", "t@t.t"], path)
    _run(["git", "config", "user.name", "t"], path)


def _commit(repo: Path, msg: str, files: dict[str, str]):
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)


def _conflicting_rebase(tmp_path: Path, rel_path: str, subject: str) -> Path:
    """Build a repo whose branch and main both edit `rel_path`, start a
    `git rebase main` on the branch, and return the repo with the conflict
    left IN PROGRESS. `subject` is the branch commit's subject (so REBASE_HEAD
    reporting can be asserted)."""
    repo = tmp_path / "repo"
    _init_repo(repo)
    _commit(repo, "base", {rel_path: "line 0\n"})

    _run(["git", "checkout", "-q", "-b", "feature"], repo)
    _commit(repo, subject, {rel_path: "line 0\nbranch edit\n"})

    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "main edit", {rel_path: "line 0\nmain edit\n"})

    _run(["git", "checkout", "-q", "feature"], repo)
    # This rebase conflicts and is left in progress (check=False).
    res = _run(["git", "rebase", "main"], repo, check=False)
    assert res.returncode != 0, "expected the rebase to conflict"
    # Sanity: the conflict is live.
    unmerged = _run(
        ["git", "diff", "--name-only", "--diff-filter=U"], repo
    ).stdout
    assert rel_path in unmerged
    return repo


def _abort(repo: Path) -> None:
    _run(["git", "rebase", "--abort"], repo, check=False)


# ---------------------------------------------------------------------------
# Contract: no internal E-NNN task IDs leak into user-facing output.
# ---------------------------------------------------------------------------

_E_TOKEN = re.compile(r"\bE-\d")


def test_source_conflict_names_files_and_offers_candidates(tmp_path):
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)

    # Names the conflicting source file.
    assert "internal/app/main.go" in msg
    # Names the step (phase), not "orphan cleanup".
    assert "rebasing your branch onto main" in msg
    assert "orphan" not in msg.lower()
    # Ambiguity flagged + at least two numbered candidate recoveries.
    assert "the wrong recovery can duplicate or lose work" in msg
    assert "1." in msg and "2." in msg
    assert "git rebase --continue" in msg  # candidate 1
    assert "reset --hard main" in msg      # candidate 2
    # Must NOT emit the confident auto-file-only prescription for a source file.
    assert "checkout main -- " not in msg
    assert "Every conflicting file is an endless-managed auto-file" not in msg
    # No internal task IDs.
    assert not _E_TOKEN.search(msg), f"leaked E-NNN token: {msg!r}"


def test_source_conflict_names_failing_commit(tmp_path):
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "my feature work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)
    # REBASE_HEAD reporting names the user's commit that failed to replay.
    assert "failed to replay" in msg
    assert "my feature work" in msg


def test_auto_file_only_conflict_gets_confident_recovery(tmp_path):
    # A conflict confined to an auto-file (matches AUTO_COMMIT_GLOBS).
    auto_path = ".endless/db-ledger/db-entries-abcd-000001.jsonl"
    repo = _conflicting_rebase(tmp_path, auto_path, "Endless: record ledger entry")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)

    assert auto_path in msg
    assert "Every conflicting file is an endless-managed auto-file" in msg
    # The confident mechanical recovery: restore the globs from base.
    assert "git -C" in msg and "checkout main -- " in msg
    for glob in AUTO_COMMIT_GLOBS:
        assert glob in msg
    # It must NOT present the ambiguous source-conflict candidates.
    assert "the wrong recovery can duplicate or lose work" not in msg
    assert not _E_TOKEN.search(msg), f"leaked E-NNN token: {msg!r}"


def test_phase_string_distinguishes_the_two_steps(tmp_path):
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        step37 = _rebase_conflict_message(
            repo, "main",
            phase="replaying your commits after dropping base auto-amend commits",
        )
        step4 = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main",
        )
    finally:
        _abort(repo)

    assert "replaying your commits after dropping" in step37
    assert "rebasing your branch onto main" in step4
    assert step37 != step4


def test_every_git_command_is_on_one_physical_line(tmp_path):
    # Copy-paste safety: no copyable `git ...` command wraps across lines.
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)
    for line in msg.splitlines():
        # Every line that starts a git command should contain the whole command;
        # we just assert no line ends mid-command with a dangling continuation.
        assert not line.rstrip().endswith("\\"), f"line continuation in: {line!r}"
