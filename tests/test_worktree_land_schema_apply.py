"""Tests for E-1941: `land` applies schema changes AFTER main advances, and
refuses a self_dev land whose branch is behind base.

On 2026-08-10 the Justfile applied this branch's schema changes BEFORE calling
`endless worktree land`. The apply succeeded, the land then failed, and the real
database was left migrated to a schema no installed binary understood — session
tracking froze machine-wide and recovery needed a hand-rolled restore. The fix is
ordering: the apply moves inside `land_worktree`, between the ff-merge (Step 5)
and the record-landing (Step 6). A failure then leaves the DB *lagging* code that
is already on main, which a re-run fixes, instead of ahead of code that never
landed.

It cannot move later than Step 6: `_record_landing` runs the same binary against
the real DB and needs the rows these changes write (E-1664 inverted). Hence the
narrow window, and hence the ordering assertions here.

Three layers:
  1. Unit — `_refuse_if_behind_base` and `_branch_schema_changes` against real
     throwaway repos.
  2. Ordering — a genuine land (real rebase + ff-merge) recording the sequence of
     apply / record calls and main's SHA at the moment the apply runs.
  3. Failure surfacing — an apply that raises must report main as advanced and
     must not unwind the merge.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    _branch_schema_changes,
    _refuse_if_behind_base,
    land_worktree,
)

CANON = "E-1941"
CHANGE = "internal/schema/changes/0099-add-thing.sql"


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _init_repo(path):
    path.mkdir(parents=True, exist_ok=True)
    _git(["git", "init", "-q", "-b", "main"], path)
    _git(["git", "config", "user.email", "t@t.t"], path)
    _git(["git", "config", "user.name", "t"], path)
    _git(["git", "config", "commit.gpgsign", "false"], path)


def _head(repo, ref="HEAD"):
    return subprocess.run(
        ["git", "rev-parse", ref], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


def _write(repo, rel, body="-- ddl\n"):
    p = Path(repo) / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(body)


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo plus a `feat` worktree, ready for a genuine land."""
    main = tmp_path / "main"
    _init_repo(main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    return {"main": main, "worktree": worktree}


def _patch_land(monkeypatch, main, worktree, *, self_dev=True):
    """Mock the DB/registry boundary so land_worktree() runs on real git."""
    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(
        "endless.config.project_is_self_dev", lambda root: self_dev
    )
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
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_endless_go", lambda wt, root: "/bin/echo"
    )
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
    monkeypatch.setattr(
        worktree_cmd, "_run_post_land_script",
        lambda wt, root, canon, sha, base: None,
    )
    monkeypatch.setattr(
        worktree_cmd, "_check_post_land_residue", lambda root, canon, before: None
    )
    monkeypatch.setattr(worktree_cmd, "_ignored_present_files", lambda root: set())


def _noop_record(item_id, proj_name, branch, base_branch, canonical,
                 merge_sha, endless_go_bin=None):
    return None


def _commit_change_on_feat(worktree, rel=CHANGE):
    _write(worktree, rel)
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "E-1941: add schema change"], worktree)


# ---------------------------------------------------------------------------
# 1. unit: the behind-base refusal
# ---------------------------------------------------------------------------

def test_current_branch_is_not_refused(landable, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    _refuse_if_behind_base(
        landable["worktree"], "main", CANON, landable["main"]
    )  # must not raise


def _advance_main_ledger_only(main, n):
    """n commits on main touching ONLY the db-ledger — what actually accumulates
    on main during any active session."""
    seg = main / ".endless" / "db-ledger" / "db-entries-aaaa-000001.jsonl"
    seg.parent.mkdir(parents=True, exist_ok=True)
    for i in range(n):
        with seg.open("a") as f:
            f.write('{"e":%d}\n' % i)
        _git(["git", "add", "-A"], main)
        _git(["git", "commit", "-q", "-m", "Endless: record ledger entry"], main)


def test_ledger_only_drift_does_not_refuse(landable, monkeypatch):
    """Regression: ledger auto-commits land on main continuously and cannot
    affect a binary. Counting them refused nearly every land — and the remedy
    the message names is a rebase, the very operation that risks the E-1943
    ledger conflict. The common case must not demand the dangerous move."""
    main, wt = landable["main"], landable["worktree"]
    _advance_main_ledger_only(main, 3)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    # Unfiltered, this branch reads as 3 behind; none of it is source.
    raw = subprocess.run(
        ["git", "rev-list", "--count", "HEAD..main"], cwd=str(wt),
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert raw == "3"
    _refuse_if_behind_base(wt, "main", CANON, main)  # must not raise


def test_mixed_commit_touching_go_still_counts(landable, monkeypatch):
    """A commit touching a ledger file AND Go source is real drift."""
    main, wt = landable["main"], landable["worktree"]
    seg = main / ".endless" / "db-ledger" / "db-entries-aaaa-000001.jsonl"
    seg.parent.mkdir(parents=True, exist_ok=True)
    seg.write_text('{"e":0}\n')
    _write(main, "internal/tasktype/kind.go", "package tasktype\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "Endless: record ledger entry"], main)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)

    with pytest.raises(click.ClickException):
        _refuse_if_behind_base(wt, "main", CANON, main)


@pytest.mark.parametrize("rel", [
    "src/endless/worktree_cmd.py",   # the land runs main's Python, not this
    "justfile",
    "tests/test_something.py",
    ".endless/decisions/ED-1551.md",
    "docs/guide/index.md",
    "README.md",
])
def test_non_binary_drift_does_not_refuse(landable, monkeypatch, rel):
    """Regression: nothing outside the Go build inputs can make endless-go
    stale, so it must not block a land. Each of these refused in an earlier
    round and blocked real work."""
    main, wt = landable["main"], landable["worktree"]
    _write(main, rel, "x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", f"change {rel}"], main)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    _refuse_if_behind_base(wt, "main", CANON, main)  # must not raise


@pytest.mark.parametrize("rel", [
    "cmd/endless-go/main.go",
    "internal/schema/changes/0100-x.sql",
    "go.mod",
    "go.sum",
])
def test_binary_input_drift_does_refuse(landable, monkeypatch, rel):
    """The hazard the guard exists for: drift in what endless-go is built from."""
    main, wt = landable["main"], landable["worktree"]
    _write(main, rel, "x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", f"change {rel}"], main)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    with pytest.raises(click.ClickException):
        _refuse_if_behind_base(wt, "main", CANON, main)


def test_behind_branch_is_refused_with_actionable_message(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    for i in range(3):
        _write(main, f"internal/pkg{i}/x.go", f"package pkg{i}\n")
        _git(["git", "add", "-A"], main)
        _git(["git", "commit", "-q", "-m", f"main {i}"], main)
    # Ledger noise alongside the real drift must not change the count.
    _advance_main_ledger_only(main, 4)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)

    with pytest.raises(click.ClickException) as ei:
        _refuse_if_behind_base(wt, "main", CANON, main)
    msg = ei.value.message
    assert "3 Go commits behind main" in msg
    assert "git rebase main" in msg
    # Must not issue a bare "rebase" instruction: under a rewritten main that
    # rebase is itself what conflicts, so the message has to name that case.
    assert ".endless/db-ledger" in msg
    assert "E-1943" in msg
    assert "reflog" in msg


def test_non_self_dev_is_never_refused(landable, monkeypatch):
    """A downstream branch being behind main is the ordinary case the land's
    rebase exists to handle — refusing there would break normal usage."""
    main, wt = landable["main"], landable["worktree"]
    for i in range(5):
        (main / "README").write_text(f"y{i}\n")
        _git(["git", "add", "-A"], main)
        _git(["git", "commit", "-q", "-m", f"main {i}"], main)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: False)
    _refuse_if_behind_base(wt, "main", CANON, main)  # must not raise


# ---------------------------------------------------------------------------
# 2. unit: the change list
# ---------------------------------------------------------------------------

def test_lists_added_sql_and_go_excluding_runner(landable):
    wt = landable["worktree"]
    _write(wt, CHANGE)
    _write(wt, "internal/schema/changes/0100-thing.go")
    _write(wt, "internal/schema/changes/runner/run.go")
    _write(wt, "internal/schema/changes/README.md")
    _write(wt, "src/unrelated.py")
    _git(["git", "add", "-A"], wt)
    _git(["git", "commit", "-q", "-m", "changes"], wt)

    found = _branch_schema_changes(wt, "main")
    assert CHANGE in found
    assert "internal/schema/changes/0100-thing.go" in found
    assert "internal/schema/changes/runner/run.go" not in found
    assert "internal/schema/changes/README.md" not in found
    assert "src/unrelated.py" not in found


def test_no_changes_is_empty(landable):
    assert _branch_schema_changes(landable["worktree"], "main") == []


def test_list_is_empty_once_main_has_been_fast_forwarded(landable):
    """Why the list is captured BEFORE Step 5: afterwards the three-dot diff
    compares a commit with itself and every change would be silently skipped."""
    wt, main = landable["worktree"], landable["main"]
    _commit_change_on_feat(wt)
    assert _branch_schema_changes(wt, "main") == [CHANGE]
    _git(["git", "merge", "--ff-only", "feat"], main)
    assert _branch_schema_changes(wt, "main") == []


# ---------------------------------------------------------------------------
# 3. ordering: apply runs after the ff-merge and before the record
# ---------------------------------------------------------------------------

def test_apply_runs_after_merge_and_before_record(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt)

    calls = []
    main_at_apply = {}

    def fake_backup(endless_go_bin=None):
        calls.append(("backup", endless_go_bin))
        return {}

    def fake_apply(path, endless_go_bin=None):
        calls.append(("apply", path))
        main_at_apply["sha"] = _head(main, "main")
        return {}

    monkeypatch.setattr("endless.event_bridge.backup_db", fake_backup)
    monkeypatch.setattr("endless.event_bridge.apply_change", fake_apply)
    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        calls.append(("record", merge_sha))

    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)

    feat_tip = _head(wt)
    land_worktree(CANON, dry_run=False)

    assert [c[0] for c in calls] == ["backup", "apply", "record"]
    # The apply saw main ALREADY advanced — the ordering the incident inverted.
    assert main_at_apply["sha"] == feat_tip
    assert calls[1][1].endswith(CHANGE)
    # The pinned worktree binary reaches both DB calls (E-1664's invariant).
    assert calls[0][1] == "/bin/echo"


def test_no_schema_changes_skips_backup_and_apply(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    (wt / "README").write_text("edited\n")
    _git(["git", "add", "-A"], wt)
    _git(["git", "commit", "-q", "-m", "E-1941: no schema change"], wt)
    _patch_land(monkeypatch, main, wt)

    calls = []
    monkeypatch.setattr(
        "endless.event_bridge.backup_db",
        lambda endless_go_bin=None: calls.append("backup"),
    )
    monkeypatch.setattr(
        "endless.event_bridge.apply_change",
        lambda path, endless_go_bin=None: calls.append("apply"),
    )
    monkeypatch.setattr(worktree_cmd, "_record_landing", _noop_record)

    land_worktree(CANON, dry_run=False)
    assert calls == []
    assert _head(main, "main") == _head(wt)


def test_non_self_dev_land_never_applies(landable, monkeypatch):
    """Downstream branches carry no endless schema changes; even if a path
    matched, a non-self_dev land must not touch the DB."""
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt, self_dev=False)
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_endless_go", lambda w, r: None
    )

    calls = []
    monkeypatch.setattr(
        "endless.event_bridge.backup_db",
        lambda endless_go_bin=None: calls.append("backup"),
    )
    monkeypatch.setattr(
        "endless.event_bridge.apply_change",
        lambda path, endless_go_bin=None: calls.append("apply"),
    )
    monkeypatch.setattr(worktree_cmd, "_record_landing", _noop_record)

    land_worktree(CANON, dry_run=False)
    assert calls == []


# ---------------------------------------------------------------------------
# 4. failure surfacing: the merge is NOT unwound
# ---------------------------------------------------------------------------

def test_apply_failure_reports_main_advanced_and_keeps_the_merge(
    landable, monkeypatch
):
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt)

    monkeypatch.setattr(
        "endless.event_bridge.backup_db", lambda endless_go_bin=None: {}
    )

    def boom(path, endless_go_bin=None):
        raise click.ClickException("no such table: thing")

    monkeypatch.setattr("endless.event_bridge.apply_change", boom)
    recorded = []

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        recorded.append(merge_sha)

    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)

    feat_tip = _head(wt)
    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert "main was advanced" in msg
    assert "no such table: thing" in msg          # original cause preserved
    assert "no restore is needed" in msg          # the point of the reorder
    assert f"just land {CANON}" in msg            # how to recover
    assert "_schema_version" in msg               # why the retry is safe
    # main really did advance and stays advanced; the record never ran.
    assert _head(main, "main") == feat_tip
    assert recorded == []


def test_backup_failure_is_also_surfaced_as_post_merge(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt)

    def boom(endless_go_bin=None):
        raise click.ClickException("disk full")

    monkeypatch.setattr("endless.event_bridge.backup_db", boom)
    applied = []
    monkeypatch.setattr(
        "endless.event_bridge.apply_change",
        lambda path, endless_go_bin=None: applied.append(path),
    )
    monkeypatch.setattr(worktree_cmd, "_record_landing", _noop_record)

    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)
    assert "main was advanced" in ei.value.message
    assert "disk full" in ei.value.message
    assert applied == []   # a failed backup must stop before migrating
