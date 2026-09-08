"""Tests for _rebase_conflict_message: what land says when a rebase conflicts.

`endless worktree land` rebases a task branch in two places (Step 3.7 orphan
replay + Step 4 main rebase); both once misattributed a content conflict —
sending the user to the wrong step with a recovery that could not work. The
shared helper reports the facts confidently: which step, which files, which
commit.

It no longer offers recoveries. It used to print two, as candidates to judge
between, and both restore the branch's side of the hunk — so a conflict whose
branch side referenced identifiers the base branch had DELETED "recovered" into
a land that succeeded and shipped code raising on first use. That is not a
wording problem, so it did not get a third wording: the message now hands off to
`endless worktree diagnose`, which classifies the captured conflict and
prescribes only what it can prove.

The one confident recovery survives, because it was never a guess: when every
conflicting file is an endless-managed auto-file, endless wrote all of them and
restoring them from the base branch loses nothing by construction.

Uses real git repos because the helper reads live rebase-conflict state
(`git diff --diff-filter=U`, REBASE_HEAD). Each test leaves a rebase in progress,
calls the helper, then aborts.
"""

import re
import subprocess
from pathlib import Path

import pytest

from endless import land_conflict
from endless.worktree_cmd import (
    AUTO_COMMIT_GLOBS,
    _rebase_branch_name,
    _rebase_conflict_message,
)


@pytest.fixture(autouse=True)
def _isolated_sandbox(tmp_path, monkeypatch):
    """The helper CAPTURES the conflict as a side effect, into the worktree's
    sandbox. Point the sandbox root somewhere disposable so a test run never
    writes to the real one."""
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / "cache"))


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


def test_source_conflict_names_files_and_hands_off_to_diagnose(tmp_path):
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
    # Hands off to the command that can classify it.
    assert "endless worktree diagnose" in msg
    # Must NOT emit the confident auto-file-only prescription for a source file.
    assert "checkout main -- " not in msg
    assert "Every conflicting file is an endless-managed auto-file" not in msg
    # No internal task IDs.
    assert not _E_TOKEN.search(msg), f"leaked E-NNN token: {msg!r}"


def test_neither_hazardous_recovery_is_offered(tmp_path):
    """The regression this task exists to prevent. Both of these put the
    branch's side of the hunk back, which is exactly what reintroduces
    identifiers the base branch deleted."""
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)

    assert "git rebase --continue" not in msg
    assert "reset --hard" not in msg
    assert "land-delta.patch" not in msg
    # And no numbered menu of any kind.
    assert not re.search(r"^\s+\d\.\s", msg, re.MULTILINE), msg


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


def test_auto_file_only_conflict_keeps_its_confident_recovery(tmp_path):
    """The one prescription that was never a guess, and is therefore kept."""
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
    assert not _E_TOKEN.search(msg), f"leaked E-NNN token: {msg!r}"


def test_git_s_own_hint_is_quoted_but_answered(tmp_path):
    """Land quotes git verbatim, and git's generic advice for any conflict is
    `git rebase --continue` — one of the two recoveries this task removed.
    Quoting it and saying nothing would hand the reader two instructions and no
    way to rank them."""
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    stderr = (
        'hint: Resolve all conflicts manually, mark them as resolved with\n'
        'hint: "git add/rm <conflicted_files>", then run "git rebase --continue".\n'
    )
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main", stderr=stderr,
        )
    finally:
        _abort(repo)

    assert "git said:" in msg
    assert "git rebase --continue" in msg          # git's words, kept
    assert "Do not follow it yet" in msg           # and answered
    # Endless's OWN prose still recommends nothing.
    ours = msg[msg.index("A source file conflicts"):]
    assert "git rebase --continue" not in ours
    assert "reset --hard" not in ours


def test_no_hint_note_when_git_did_not_suggest_continuing(tmp_path):
    """The note answers a specific hint. Printed unconditionally it would warn
    about advice nobody was given."""
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main",
            stderr="error: could not apply abc1234... user work\n",
        )
    finally:
        _abort(repo)

    assert "git said:" in msg
    assert "Do not follow it yet" not in msg


def test_the_message_captures_the_conflict_before_the_abort(tmp_path):
    """Building the message and recording the evidence are one act, so a future
    third conflict handler cannot report a conflict without recording it."""
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)

    ev = land_conflict.load_evidence(repo)
    assert ev is not None
    assert ev.unmerged_paths == ["internal/app/main.go"]
    assert ev.branch == "feature"
    assert ev.rebase_head_subject == "user work"
    assert ev.hunks and "branch edit" in ev.hunks[0].branch_side


def test_the_message_says_so_when_the_capture_could_not_be_written(
    tmp_path, monkeypatch
):
    """Sending someone to `diagnose` after failing to write the capture would
    have them told there is nothing recorded — which reads as the tool losing
    their conflict rather than as a directory it could not write."""
    blocked = tmp_path / "not-a-dir"
    blocked.write_text("")
    monkeypatch.setattr(
        land_conflict, "evidence_path", lambda _wt: blocked / "conflict.json")

    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        msg = _rebase_conflict_message(
            repo, "main", phase="rebasing your branch onto main"
        )
    finally:
        _abort(repo)

    assert "could NOT be written" in msg
    # It still NAMES diagnose — to say it will have nothing to read. What it
    # must not do is instruct someone to go and run it.
    assert "Classify it before you touch anything" not in msg
    assert "has nothing to read" in msg
    # And still no hazardous recovery, capture or no capture.
    assert "git rebase --continue" not in msg
    assert "reset --hard" not in msg


def test_rebase_branch_name_reads_through_a_detached_head(tmp_path):
    """HEAD is detached partway through a replay, so it names the machinery.
    The branch being rebased is what the capture has to record."""
    repo = _conflicting_rebase(tmp_path, "internal/app/main.go", "user work")
    try:
        assert _run(["git", "rev-parse", "--abbrev-ref", "HEAD"],
                    repo).stdout.strip() == "HEAD"
        assert _rebase_branch_name(repo) == "feature"
    finally:
        _abort(repo)
    # And outside a rebase it falls back to the checked-out branch.
    assert _rebase_branch_name(repo) == "feature"


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
