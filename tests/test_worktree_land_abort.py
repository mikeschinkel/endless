"""Tests for E-2295: land says the rebase was aborted only when it was.

Landing E-1562 hit a source conflict and reported "The rebase has been aborted
and your branch is exactly as it was", then sent the agent to `endless worktree
diagnose`. Neither was true of the worktree it left: it was still mid-rebase,
HEAD detached at main's tip, one file in conflict — and `diagnose` refused,
because a detached HEAD has no branch to look up.

The cause was the abort's result going unread. Land ran `git rebase --abort`
with check=False and raised a refusal written before the abort had run. A
`verify` run was live in the same worktree, so the abort most plausibly lost the
index lock — the race E-2174 retries for the rebase itself but not the abort.

End-to-end against real git. The failing abort is a REAL held `index.lock`,
taken at the moment land calls `git rebase --abort`, not a stubbed return code.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    LAND_MAX_RETRIES,
    _rebase_in_progress,
    land_worktree,
)

CANON = "E-2295"


def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _out(cmd, cwd):
    return subprocess.run(
        cmd, cwd=str(cwd), capture_output=True, text=True, check=True,
    ).stdout.strip()


def _init_repo(path):
    path.mkdir(parents=True, exist_ok=True)
    _git(["git", "init", "-q", "-b", "main"], path)
    _git(["git", "config", "user.email", "t@t.t"], path)
    _git(["git", "config", "user.name", "t"], path)
    _git(["git", "config", "commit.gpgsign", "false"], path)


@pytest.fixture
def conflicting(tmp_path):
    """A real `main` repo + a `feat` worktree whose one commit conflicts with
    main's on the same source file, so Step 4's rebase stops mid-flight."""
    main = tmp_path / "main"
    _init_repo(main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "clash.py").write_text("base\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)

    (main / "clash.py").write_text("main side\n")
    _git(["git", "commit", "-q", "-am", "main clash"], main)
    (worktree / "clash.py").write_text("feat side\n")
    _git(["git", "commit", "-q", "-am", "feat clash"], worktree)

    return {"main": main, "worktree": worktree, "feat_tip": _out(
        ["git", "rev-parse", "HEAD"], worktree)}


def _index_lock(worktree: Path) -> Path:
    git_dir = _out(["git", "rev-parse", "--absolute-git-dir"], worktree)
    return Path(git_dir) / "index.lock"


def _patch_land(monkeypatch, main, worktree):
    """DB/registry-boundary mocks so land_worktree() runs on real git."""
    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [{"_": 1}])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task",
        lambda rows, canonical: {
            "branch": "feat", "path": str(worktree),
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
    monkeypatch.setattr(worktree_cmd, "_record_landing", lambda *a, **k: None)


def _lock_on_abort(monkeypatch, worktree):
    """Take the worktree's index lock the moment land asks git to abort, the
    way a concurrent `git status` from a verify run would."""
    lock = _index_lock(worktree)
    real = worktree_cmd._git_run
    taken = []

    def wrapped(args, cwd, check=True):
        if list(args) == ["rebase", "--abort"] and not taken:
            taken.append(True)
            lock.write_text("")
        return real(args, cwd, check=check)

    monkeypatch.setattr(worktree_cmd, "_git_run", wrapped)
    return lock


def _no_sleep(monkeypatch, on_backoff=None):
    waits = []

    def fake(attempt):
        waits.append(attempt)
        if on_backoff is not None:
            on_backoff(attempt)

    monkeypatch.setattr(worktree_cmd, "_lock_backoff", fake)
    return waits


def _land_refusal(main, worktree) -> str:
    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)
    return ei.value.message


def test_abort_that_never_takes_names_the_rebase_still_in_progress(
    conflicting, monkeypatch,
):
    # The E-1562 reproduction: the abort loses the lock and never gets it back.
    main, worktree = conflicting["main"], conflicting["worktree"]
    _patch_land(monkeypatch, main, worktree)
    lock = _lock_on_abort(monkeypatch, worktree)
    waits = _no_sleep(monkeypatch)

    msg = _land_refusal(main, worktree)

    # The worktree really is left mid-rebase — the fixture reproduces the field.
    lock.unlink()
    assert _rebase_in_progress(worktree)
    assert _out(["git", "rev-parse", "--abbrev-ref", "HEAD"], worktree) == "HEAD"

    # So the refusal must not claim otherwise ...
    assert "has been aborted" not in msg
    assert "exactly as it was" not in msg
    # ... must not send the reader to a command that refuses on detached HEAD ...
    assert "Classify it before you touch anything" not in msg
    assert "diagnose` cannot help until then" in msg
    # ... and must say what is true and how to clear it.
    assert "MID-REBASE" in msg
    assert "detached" in msg
    assert "git rebase --abort" in msg
    assert "index.lock" in msg                       # git's own words
    assert "do NOT delete the lock file" in msg      # ... and not git's hint
    assert "Still in conflict:\n  clash.py" in msg   # read live, not assumed
    assert "endless worktree land" in msg
    # The abort was retried the way E-2174 retries the rebase, not tried once.
    assert len(waits) == LAND_MAX_RETRIES - 1

    _git(["git", "rebase", "--abort"], worktree)


def test_abort_that_wins_the_lock_on_retry_reports_the_branch_unchanged(
    conflicting, monkeypatch,
):
    # The holder lets go after one wait. The abort then takes, and the
    # original report — branch unchanged, conflict recorded, diagnose it — is
    # the truth again.
    main, worktree = conflicting["main"], conflicting["worktree"]
    _patch_land(monkeypatch, main, worktree)
    lock = _lock_on_abort(monkeypatch, worktree)
    waits = _no_sleep(monkeypatch, on_backoff=lambda a: lock.unlink())

    msg = _land_refusal(main, worktree)

    assert waits == [1]
    assert not _rebase_in_progress(worktree)
    assert _out(["git", "rev-parse", "--abbrev-ref", "HEAD"], worktree) == "feat"
    assert _out(["git", "rev-parse", "HEAD"], worktree) == conflicting["feat_tip"]
    assert "has been aborted" in msg
    assert "endless worktree diagnose" in msg
    assert "MID-REBASE" not in msg


def test_abort_with_no_contention_reports_the_branch_unchanged(
    conflicting, monkeypatch,
):
    main, worktree = conflicting["main"], conflicting["worktree"]
    _patch_land(monkeypatch, main, worktree)
    waits = _no_sleep(monkeypatch)

    msg = _land_refusal(main, worktree)

    assert waits == []
    assert not _rebase_in_progress(worktree)
    assert _out(["git", "rev-parse", "HEAD"], worktree) == conflicting["feat_tip"]
    assert "has been aborted" in msg
    assert "MID-REBASE" not in msg


# ─── the sibling abort: `worktree sync --apply` ────────────────────────────────

def _patch_sync(monkeypatch, main, worktree):
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_default_base_branch", lambda root: "main")
    monkeypatch.setattr(worktree_cmd, "worktree_root_for_cwd", lambda: None)
    monkeypatch.setattr(
        worktree_cmd, "_enriched_list",
        lambda root: [{"state": "active", "path": str(worktree), "branch": "feat"}],
    )
    monkeypatch.setattr(
        worktree_cmd, "_sync_state", lambda path, base, here: ("rebase", "behind main"),
    )


def test_sync_does_not_report_a_stuck_worktree_as_restored(
    conflicting, monkeypatch, capsys,
):
    main, worktree = conflicting["main"], conflicting["worktree"]
    _patch_sync(monkeypatch, main, worktree)
    lock = _lock_on_abort(monkeypatch, worktree)
    _no_sleep(monkeypatch)

    worktree_cmd.sync_worktrees(apply=True)
    out = capsys.readouterr().out

    lock.unlink()
    assert _rebase_in_progress(worktree)
    assert "— aborted:" not in out
    assert "was restored" not in out
    assert "STUCK" in out and "NOT aborted" in out
    assert "MID-REBASE" in out
    assert "rebase --abort   # feat" in out
    assert "index.lock" in out                       # git's own words

    _git(["git", "rebase", "--abort"], worktree)


def test_sync_still_reports_a_clean_abort_as_restored(
    conflicting, monkeypatch, capsys,
):
    main, worktree = conflicting["main"], conflicting["worktree"]
    _patch_sync(monkeypatch, main, worktree)
    _no_sleep(monkeypatch)

    worktree_cmd.sync_worktrees(apply=True)
    out = capsys.readouterr().out

    assert not _rebase_in_progress(worktree)
    assert "— aborted:" in out
    assert "was restored" in out
    assert "STUCK" not in out


# ─── diagnose on the worktree a stuck abort leaves ─────────────────────────────

def test_diagnose_names_the_rebase_in_progress_instead_of_a_bare_detached_head(
    conflicting, monkeypatch,
):
    main, worktree = conflicting["main"], conflicting["worktree"]
    subprocess.run(["git", "rebase", "main"], cwd=str(worktree),
                   capture_output=True)
    assert _rebase_in_progress(worktree)

    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [{"_": 1}])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task",
        lambda rows, canonical: {"branch": "", "path": str(worktree),
                                 "companion": {"base_branch": "main"}},
    )

    with pytest.raises(click.ClickException) as ei:
        worktree_cmd.diagnose_land_conflict(CANON, as_json=False)
    msg = ei.value.message

    assert "MID-REBASE of feat" in msg
    assert "rebase --abort" in msg
    assert f"endless worktree land {CANON}" in msg
    assert "nothing to diagnose. Nothing was changed" not in msg  # the old text
    assert _rebase_in_progress(worktree), "diagnose must not abort it"

    _git(["git", "rebase", "--abort"], worktree)
