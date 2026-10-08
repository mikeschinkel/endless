"""Tests for E-2275: the recorder committing main's managed files first must
not fail a land.

Step 3 of `land_worktree` reads main's `git status`, then stages and commits the
Endless-managed files it found. Endless's background recorder
(internal/events/commit.go) commits the same paths on main on its own schedule
and shares no lock with the land. When it commits in between, the land's
`git commit` has nothing left to commit: git exits 1, says so on stdout, and
leaves stderr empty. Seen landing E-2158 on 2026-10-08.

Everything here runs against a real throwaway repo. The race is made
deterministic by wrapping `_git_run` so the recorder's commit happens exactly
between the land's `git add` and its `git commit` — the git calls themselves
are real, so the failure shape is git's, not an assumption.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import land_worktree

CANON = "E-2275"
LEDGER_A = ".endless/db-ledger/a.jsonl"
LEDGER_B = ".endless/db-ledger/b.jsonl"
RECORDER_SUBJECT = "Endless: record ledger entry"
LAND_SUBJECT = "Endless: auto-record session activity"


def _git(cmd, cwd):
    return subprocess.run(
        cmd, cwd=str(cwd), check=True, capture_output=True, text=True,
    ).stdout


def _init_repo(path):
    path.mkdir(parents=True, exist_ok=True)
    _git(["git", "init", "-q", "-b", "main"], path)
    _git(["git", "config", "user.email", "t@t.t"], path)
    _git(["git", "config", "user.name", "t"], path)
    _git(["git", "config", "commit.gpgsign", "false"], path)


def _subjects(repo, n):
    return _git(["git", "log", "--format=%s", f"-{n}"], repo).splitlines()


def _recorder_commit(main, paths):
    """Commit `paths` on main the way internal/events/commit.go does."""
    _git(["git", "add", "--", *paths], main)
    _git(["git", "commit", "-q", "-m", RECORDER_SUBJECT, "--", *paths], main)


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo with two dirty ledger segments, plus a `feat`
    worktree with one commit to land."""
    main = tmp_path / "main"
    _init_repo(main)
    (main / ".endless" / "db-ledger").mkdir(parents=True)
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / LEDGER_A).write_text('{"e":1}\n')
    (main / LEDGER_B).write_text('{"e":1}\n')
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    (worktree / "on-feat.txt").write_text("f\n")
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "branch work"], worktree)

    # The recorder's pending appends: Step 1 sees these as managed files.
    (main / LEDGER_A).write_text('{"e":1}\n{"e":2}\n')
    (main / LEDGER_B).write_text('{"e":1}\n{"e":2}\n')
    return {"main": main, "worktree": worktree}


def _patch_land(monkeypatch, main, worktree, recorded):
    """DB/registry-boundary mocks so land_worktree() runs on real git."""
    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [{"_": 1}])
    monkeypatch.setattr(
        worktree_cmd,
        "_branch_for_task",
        lambda rows, canonical: {
            "branch": "feat",
            "path": str(worktree),
            "companion": {"base_branch": "main"},
        },
    )
    monkeypatch.setattr(worktree_cmd, "_resolve_land_endless_go", lambda wt, root: None)
    monkeypatch.setattr(
        worktree_cmd, "_dedup_worktree_verbs_against_main", lambda wt, root: False
    )
    monkeypatch.setattr(
        worktree_cmd, "_drop_orphan_amendable_commits", lambda wt, base: (0, "")
    )
    monkeypatch.setattr(worktree_cmd, "_ledger_touching_commits", lambda wt, base: [])
    monkeypatch.setattr(
        worktree_cmd, "_guard_modified_worktree", lambda wt, branch, canon: None
    )
    monkeypatch.setattr(worktree_cmd, "_resolve_project", lambda arg: (None, "p"))
    monkeypatch.setattr(worktree_cmd, "_reap_stale_worktrees", lambda root: None)
    monkeypatch.setattr(worktree_cmd, "_lock_backoff", lambda attempt: None)

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        recorded["merge_sha"] = merge_sha

    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)


def _race_before_land_commit(monkeypatch, main, recorder_paths, seen):
    """Run the recorder's commit of `recorder_paths` right before the land's
    managed-file commit, and capture that commit's failure, if any."""
    real = worktree_cmd._git_run

    def racing(args, cwd, check=True):
        if args[:1] == ["commit"] and LAND_SUBJECT in args and Path(cwd) == main:
            if not seen.get("raced"):
                seen["raced"] = True
                _recorder_commit(main, recorder_paths)
            try:
                return real(args, cwd, check)
            except subprocess.CalledProcessError as e:
                seen["error"] = e
                raise
        return real(args, cwd, check)

    monkeypatch.setattr(worktree_cmd, "_git_run", racing)


def test_race_failure_shape_is_exit_1_with_empty_stderr(landable, monkeypatch):
    # The plan's step 1: prove the failure shape rather than assume it. The
    # recorder took every managed file, so the land's commit has nothing left.
    main, worktree = landable["main"], landable["worktree"]
    seen, recorded = {}, {}
    _patch_land(monkeypatch, main, worktree, recorded)
    _race_before_land_commit(monkeypatch, main, [LEDGER_A, LEDGER_B], seen)

    try:
        land_worktree(CANON, dry_run=False)
    except click.ClickException:
        pass   # before E-2275 the land refused here; the shape is under test

    e = seen["error"]
    assert e.returncode == 1
    assert e.stderr == ""
    assert "nothing" in e.stdout and "commit" in e.stdout


def test_full_race_lands_with_the_recorders_commit_and_no_duplicate(
    landable, monkeypatch, capsys
):
    main, worktree = landable["main"], landable["worktree"]
    seen, recorded = {}, {}
    _patch_land(monkeypatch, main, worktree, recorded)
    _race_before_land_commit(monkeypatch, main, [LEDGER_A, LEDGER_B], seen)

    land_worktree(CANON, dry_run=False)   # must NOT raise

    assert recorded["merge_sha"] == _git(["git", "rev-parse", "HEAD"], main).strip()
    assert _subjects(main, 3) == ["branch work", RECORDER_SUBJECT, "init"]
    assert _git(["git", "status", "--porcelain"], main) == ""
    assert f"Landed {CANON}" in capsys.readouterr().out


def test_partial_race_commits_the_rest_and_lands(landable, monkeypatch):
    # The recorder took one of the two managed files; the land's commit still
    # has the other staged, so it succeeds as an ordinary commit.
    main, worktree = landable["main"], landable["worktree"]
    seen, recorded = {}, {}
    _patch_land(monkeypatch, main, worktree, recorded)
    _race_before_land_commit(monkeypatch, main, [LEDGER_A], seen)

    land_worktree(CANON, dry_run=False)

    assert "error" not in seen
    assert _subjects(main, 4) == [
        "branch work", LAND_SUBJECT, RECORDER_SUBJECT, "init",
    ]
    assert _git(
        ["git", "show", "--name-only", "--format=", "HEAD~1"], main,
    ).split() == [LEDGER_B]
    assert _git(["git", "status", "--porcelain"], main) == ""


def test_real_commit_refusal_still_refuses(landable, monkeypatch):
    # A failing pre-commit hook leaves the files staged — that is a real
    # refusal, not the race, and must be reported exactly as before.
    main, worktree = landable["main"], landable["worktree"]
    hook = main / ".git" / "hooks" / "pre-commit"
    hook.write_text("#!/bin/sh\necho 'hook says no' >&2\nexit 1\n")
    hook.chmod(0o755)
    head_before = _git(["git", "rev-parse", "HEAD"], main)
    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)

    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.format_message()
    assert msg.startswith("auto-commit failed:")
    assert "hook says no" in msg
    assert "merge_sha" not in recorded
    assert _git(["git", "rev-parse", "HEAD"], main) == head_before


def test_stdout_only_failure_shows_its_stdout(landable, monkeypatch):
    # Git sometimes explains a refusal on stdout alone. The refusal must carry
    # those words rather than "act on what git said below" above nothing.
    main, worktree = landable["main"], landable["worktree"]
    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    real = worktree_cmd._git_run

    def stdout_only(args, cwd, check=True):
        if args[:1] == ["commit"] and LAND_SUBJECT in args:
            raise subprocess.CalledProcessError(
                1, ["git", *args], output="git explains on stdout\n", stderr="",
            )
        return real(args, cwd, check)

    monkeypatch.setattr(worktree_cmd, "_git_run", stdout_only)
    # The files stay staged, so this is not mistaken for the race.
    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.format_message()
    assert msg.startswith("auto-commit failed:")
    assert "git explains on stdout" in msg
    assert "merge_sha" not in recorded
