"""Tests for E-2020: a self_dev land records its landing with the INSTALLED
endless-go, rebuilt from the advanced main — never with the worktree's build.

ED-1601: a binary built in a task worktree never opens the main database. The
land used to record `task.landed` with the worktree's own endless-go (E-1664),
because only that build matched the rows the land had just migrated. That
binary is now refused main outright, so the land:

  1. resolves the main checkout's `bin/endless-go` as the recording binary,
     refusing BEFORE the merge when it cannot be rebuilt (no `just`);
  2. rebuilds it at Step 5.6 — after the migration, before the record;
  3. reports a failed rebuild as post-merge and re-runnable, with nothing
     recorded.
"""

import subprocess

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    _rebuild_main_binary,
    _resolve_land_endless_go,
    land_worktree,
)

CANON = "E-2020"


def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _head(repo, ref="HEAD"):
    return subprocess.run(
        ["git", "rev-parse", ref], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


@pytest.fixture
def landable(tmp_path):
    main = tmp_path / "main"
    main.mkdir()
    _git(["git", "init", "-q", "-b", "main"], main)
    for k, v in (("user.email", "t@t.t"), ("user.name", "t"),
                 ("commit.gpgsign", "false")):
        _git(["git", "config", k, v], main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)
    wt = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(wt), "-b", "feat", "main"], main)
    (wt / "thing.py").write_text("x = 1\n")
    _git(["git", "add", "-A"], wt)
    _git(["git", "commit", "-q", "-m", "E-2020: work"], wt)
    return main, wt


def _patch_land(monkeypatch, main, wt, calls, *, rebuild=None):
    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [{"_": 1}])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task",
        lambda rows, canonical: {
            "branch": "feat", "path": str(wt),
            "companion": {"base_branch": "main"},
        },
    )
    for name, fn in (
        ("_dedup_worktree_verbs_against_main", lambda w, r: False),
        ("_drop_orphan_amendable_commits", lambda w, b: (0, "")),
        ("_ledger_touching_commits", lambda w, b: []),
        ("_guard_modified_worktree", lambda w, b, c: None),
        ("_resolve_project", lambda arg: (None, "p")),
        ("_reap_stale_worktrees", lambda root: None),
        ("_run_post_land_script", lambda w, r, c, s, b: None),
        ("_check_post_land_residue", lambda r, c, b: None),
        ("_ignored_present_files", lambda root: set()),
        ("_rebuild_worktree_binary", lambda w, c: None),
        ("_build_migration_executable", lambda w, c: None),
        ("_resolve_land_migrate_bin", lambda w, r: "/wt/bin/endless-migrate"),
        ("_warm_unlanded_cache", lambda w: None),
    ):
        monkeypatch.setattr(worktree_cmd, name, fn)
    monkeypatch.setattr("shutil.which", lambda name: f"/usr/bin/{name}")

    def migrate(*a, **kw):
        calls.append(("migrate", None))

    def record(*a, **kw):
        calls.append(("record", kw.get("endless_go_bin")))

    monkeypatch.setattr(worktree_cmd, "_apply_branch_schema_changes", migrate)
    monkeypatch.setattr(worktree_cmd, "_record_landing", record)
    monkeypatch.setattr(
        worktree_cmd, "_rebuild_main_binary",
        rebuild or (lambda m, c, b: calls.append(("rebuild", str(m)))),
    )


def test_the_recording_binary_is_the_main_checkouts(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    monkeypatch.setattr("shutil.which", lambda name: "/usr/bin/just")
    got = _resolve_land_endless_go(tmp_path / "wt", tmp_path / "main")
    assert got == str(tmp_path / "main" / "bin" / "endless-go")


def test_no_just_refuses_before_the_merge(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    monkeypatch.setattr("shutil.which", lambda name: None)
    with pytest.raises(click.ClickException) as exc:
        _resolve_land_endless_go(tmp_path / "wt", tmp_path / "main")
    assert "`just` is not on PATH" in exc.value.message


def test_non_self_dev_records_with_the_global(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: False)
    assert _resolve_land_endless_go(tmp_path / "wt", tmp_path / "main") is None


def test_migrate_then_rebuild_then_record_with_the_installed_binary(
        landable, monkeypatch):
    main, wt = landable
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    land_worktree(CANON, dry_run=False)

    assert [c[0] for c in calls] == ["migrate", "rebuild", "record"]
    assert dict(calls)["rebuild"] == str(main)
    assert dict(calls)["record"] == str(main / "bin" / "endless-go")
    assert "/.endless/worktrees/" not in dict(calls)["record"]


def test_a_failed_rebuild_is_post_merge_and_records_nothing(landable, monkeypatch):
    main, wt = landable
    calls = []

    def broken(m, c, b):
        raise click.ClickException("rebuild broke")

    _patch_land(monkeypatch, main, wt, calls, rebuild=broken)
    before = _head(main, "main")

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)

    assert _head(main, "main") != before, "main advanced before the rebuild"
    assert [c[0] for c in calls] == ["migrate"]


def test_rebuild_failure_message_is_rerunnable(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)

    class Result:
        returncode = 1
        stderr = "compile error"
        stdout = ""

    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: Result())
    with pytest.raises(click.ClickException) as exc:
        _rebuild_main_binary(tmp_path, CANON, "main")
    msg = exc.value.message
    assert "compile error" in msg
    assert "not recorded yet" in msg
    assert f"just land {CANON}" in msg


def test_rebuild_runs_just_go_in_the_main_checkout(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    seen = {}

    class Result:
        returncode = 0
        stderr = stdout = ""

    def run(cmd, cwd=None, **kw):
        seen["cmd"], seen["cwd"] = cmd, cwd
        return Result()

    monkeypatch.setattr(subprocess, "run", run)
    _rebuild_main_binary(tmp_path, CANON, "main")
    assert seen == {"cmd": ["just", "go"], "cwd": str(tmp_path)}
