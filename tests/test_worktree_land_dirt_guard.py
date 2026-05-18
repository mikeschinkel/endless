"""Tests for E-1416 step 3.8: _guard_dirty_worktree refuses land when the
worktree's tree has uncommitted files, with separate messages for
auto-managed dirt (upstream writer bug) vs unmanaged user dirt.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless.worktree_cmd import _guard_dirty_worktree


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def worktree_repo(tmp_path):
    """Initialize a git repo at tmp_path with one commit; treat it as a
    worktree for these tests (the guard only cares about the dir, not
    whether it's a real linked worktree).

    Pre-commits the canonical .endless/ subdirectories so that adding
    untracked files inside them surfaces as individual paths in
    'git status --porcelain' rather than collapsing to the parent dir.
    Mirrors a real endless project's on-disk layout.
    """
    repo = tmp_path / "wt"
    repo.mkdir()
    _run(["git", "init", "-q", "-b", "feat"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("init\n")
    for sub in (".endless", ".endless/plans", ".endless/plans/snapshots",
                ".endless/db-ledger"):
        d = repo / sub
        d.mkdir(parents=True, exist_ok=True)
        (d / ".gitkeep").write_text("")
    (repo / ".endless" / "verbs.jsonl").write_text("")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)
    return repo


def test_clean_worktree_passes(worktree_repo):
    # No exception means the guard let land continue.
    _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")


def test_auto_managed_dirt_refuses_with_writer_message(worktree_repo):
    # Snapshot pair (matches .endless/plans/snapshots/* glob).
    snaps = worktree_repo / ".endless" / "plans" / "snapshots"
    (snaps / "20260518T000000-abcd1234.md").write_text("# snap\n")
    (snaps / "20260518T000000-abcd1234.json").write_text("{}\n")

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    assert "uncommitted auto-managed files" in msg
    assert "E-1416" in msg
    assert "20260518T000000-abcd1234.md" in msg
    assert "20260518T000000-abcd1234.json" in msg
    assert "Report the writer" in msg


def test_unmanaged_dirt_refuses_with_recovery_hints(worktree_repo):
    # A non-auto-managed dirty file (e.g., a source file).
    (worktree_repo / "main.go").write_text("package main\n")

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    assert "uncommitted user changes" in msg
    assert "E-1416" in msg
    assert "main.go" in msg
    assert "commit on feat" in msg
    assert "move the file aside" in msg
    assert "git checkout --" in msg


def test_auto_managed_dirt_wins_when_both_kinds_present(worktree_repo):
    # Auto-managed (snapshot) AND unmanaged (source) dirt both present.
    snaps = worktree_repo / ".endless" / "plans" / "snapshots"
    (snaps / "20260518T000000-abcd1234.md").write_text("# snap\n")
    (worktree_repo / "main.go").write_text("package main\n")

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    # Auto-managed message wins because it's checked first.
    assert "uncommitted auto-managed files" in msg
    assert "20260518T000000-abcd1234.md" in msg
    # Unmanaged file is NOT in the auto-managed message — it would surface
    # on retry after the writer bug is fixed and the snapshot committed.
    assert "main.go" not in msg


def test_db_ledger_dirt_is_auto_managed(worktree_repo):
    # .endless/db-ledger/*.jsonl is in AUTO_COMMIT_GLOBS.
    ledger = worktree_repo / ".endless" / "db-ledger"
    (ledger / "db-entries-abcdef-000001.jsonl").write_text('{"e": 1}\n')

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    assert "uncommitted auto-managed files" in msg
    assert "db-entries-abcdef-000001.jsonl" in msg


def test_verbs_jsonl_dirt_is_auto_managed(worktree_repo):
    # .endless/verbs.jsonl is in AUTO_COMMIT_GLOBS. The fixture commits
    # an empty file; modifying it is auto-managed dirt.
    (worktree_repo / ".endless" / "verbs.jsonl").write_text('{"value": "x"}\n')

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    assert "uncommitted auto-managed files" in exc.value.message
    assert "verbs.jsonl" in exc.value.message


def test_modified_tracked_file_refuses(worktree_repo):
    # Modify the already-tracked README; should refuse as unmanaged dirt.
    (worktree_repo / "README.md").write_text("modified\n")

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    assert "uncommitted user changes" in msg
    assert "README.md" in msg


def test_many_files_get_truncated_in_message(worktree_repo):
    # 25 untracked source files → message should list 20, then "... and 5 more".
    for i in range(25):
        (worktree_repo / f"file_{i:02d}.go").write_text("x\n")

    with pytest.raises(click.ClickException) as exc:
        _guard_dirty_worktree(worktree_repo, branch="feat", canonical="E-1416")
    msg = exc.value.message
    assert "uncommitted user changes" in msg
    assert "... and 5 more" in msg
