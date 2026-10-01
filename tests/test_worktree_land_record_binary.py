"""Tests for E-2020: a self_dev land records its landing with the INSTALLED
endless-go, rebuilt from the advanced main — never with the worktree's build.

ED-1601: a binary built in a task worktree never opens the main database. The
land used to record `task.landed` with the worktree's own endless-go (E-1664),
because only that build matched the rows the land had just migrated. That
binary is now refused main outright, so the land:

  1. resolves the main checkout's `bin/endless-go` as the recording binary,
     refusing BEFORE the merge when it cannot be rebuilt (no `just`);
  2. builds it to the side at Step 5.2 — after the merge, BEFORE the
     migration — and swaps it in at Step 5.6, right after the migration and
     before the record (E-2205: the compile no longer holds the old binary in
     place against a migrated database, where every reader recorded ERR-0020);
  3. reports a failed build as post-merge, pre-migration and re-runnable, and
     a failed swap as post-migration and re-runnable, with nothing recorded.
"""

import subprocess

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    _build_main_binary_next,
    _resolve_land_endless_go,
    _swap_main_binary,
    land_worktree,
)

CANON = "E-2020"
UP_MIGRATED = {"status": "migrated", "from": 11, "to": 12, "db": "/x/endless.db"}


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


def _patch_land(monkeypatch, main, wt, calls, *, build=None, swap=None):
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
        # E-2184's gate shells to the installed endless-go; not this test's subject.
        ("_refuse_if_land_gated", lambda r, w, b, c: None),
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
        return UP_MIGRATED

    def record(*a, **kw):
        calls.append(("record", kw.get("endless_go_bin")))

    monkeypatch.setattr(worktree_cmd, "_migrate_landed_schema", migrate)
    monkeypatch.setattr(worktree_cmd, "_record_landing", record)
    monkeypatch.setattr(
        worktree_cmd, "_clear_land_schema_faults",
        lambda up, since, c, bin_: calls.append(("clear", (up, bin_))),
    )
    monkeypatch.setattr(
        worktree_cmd, "_build_main_binary_next",
        build or (lambda m, c, b: calls.append(("build", str(m)))),
    )
    monkeypatch.setattr(
        worktree_cmd, "_swap_main_binary",
        swap or (lambda m, c, b: calls.append(("swap", str(m)))),
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


def test_build_then_migrate_then_swap_then_record_with_the_installed_binary(
        landable, monkeypatch):
    main, wt = landable
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    land_worktree(CANON, dry_run=False)

    # E-2205: the build precedes the migration and nothing builds after it —
    # the only step between migrating and recording is the swap.
    assert [c[0] for c in calls] == ["build", "migrate", "swap", "record", "clear"]
    assert dict(calls)["build"] == str(main)
    # The clear gets the migration's versions and the recording binary.
    assert dict(calls)["clear"] == (UP_MIGRATED, str(main / "bin" / "endless-go"))
    assert dict(calls)["swap"] == str(main)
    assert dict(calls)["record"] == str(main / "bin" / "endless-go")
    assert "/.endless/worktrees/" not in dict(calls)["record"]


def test_a_failed_build_stops_before_the_migration(landable, monkeypatch):
    main, wt = landable
    calls = []

    def broken(m, c, b):
        raise click.ClickException("build broke")

    _patch_land(monkeypatch, main, wt, calls, build=broken)
    before = _head(main, "main")

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)

    assert _head(main, "main") != before, "main advanced before the build"
    assert calls == [], "nothing migrated, swapped or recorded"


def test_a_failed_swap_is_post_migration_and_records_nothing(landable, monkeypatch):
    main, wt = landable
    calls = []

    def broken(m, c, b):
        raise click.ClickException("swap broke")

    _patch_land(monkeypatch, main, wt, calls, swap=broken)

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)

    assert [c[0] for c in calls] == ["build", "migrate"]


class _Failed:
    returncode = 1
    stderr = "compile error"
    stdout = ""


class _Ok:
    returncode = 0
    stderr = stdout = ""


def test_build_failure_message_says_nothing_migrated_and_names_the_rerun(
        tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: _Failed())
    with pytest.raises(click.ClickException) as exc:
        _build_main_binary_next(tmp_path, CANON, "main")
    msg = exc.value.message
    assert "compile error" in msg
    assert "Neither the database migration nor the landing record" in msg
    assert f"just land {CANON}" in msg


def test_swap_failure_message_is_rerunnable(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    # No bin/endless-go.next: the rename fails.
    with pytest.raises(click.ClickException) as exc:
        _swap_main_binary(tmp_path, CANON, "main")
    msg = exc.value.message
    assert "endless-go.next" in msg
    assert "migrated the database" in msg
    assert "not recorded yet" in msg
    assert f"just land {CANON}" in msg


def test_swap_renames_the_built_binary_over_the_installed_one(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    (tmp_path / "bin").mkdir()
    (tmp_path / "bin" / "endless-go").write_text("old\n")
    (tmp_path / "bin" / "endless-go.next").write_text("new\n")

    _swap_main_binary(tmp_path, CANON, "main")

    assert (tmp_path / "bin" / "endless-go").read_text() == "new\n"
    assert not (tmp_path / "bin" / "endless-go.next").exists()


def test_build_runs_go_build_next_in_the_main_checkout(tmp_path, monkeypatch):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    seen = {}

    def run(cmd, cwd=None, **kw):
        seen["cmd"], seen["cwd"] = cmd, cwd
        return _Ok()

    monkeypatch.setattr(subprocess, "run", run)
    _build_main_binary_next(tmp_path, CANON, "main")
    assert seen == {"cmd": ["just", "go-build-next"], "cwd": str(tmp_path)}


@pytest.mark.parametrize("fn", [_build_main_binary_next, _swap_main_binary])
def test_build_and_swap_do_nothing_outside_self_dev(tmp_path, monkeypatch, fn):
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: False)

    def run(*a, **kw):
        raise AssertionError("ran a build outside self_dev")

    monkeypatch.setattr(subprocess, "run", run)
    fn(tmp_path, CANON, "main")  # no bin/ at all: a swap attempt would raise


# --- E-2205: the land clears the ERR-0020 its own migration caused ----------

def test_clear_names_both_versions_the_window_and_the_land(monkeypatch):
    seen = {}

    def clear(db_version, binary_version, since, by, endless_go_bin=None):
        seen.update(db=db_version, binary=binary_version, since=since, by=by,
                    bin=endless_go_bin)
        return 1

    monkeypatch.setattr("endless.event_bridge.clear_land_schema_faults", clear)
    worktree_cmd._clear_land_schema_faults(
        UP_MIGRATED, "2026-10-03T12:00:00", CANON, "/main/bin/endless-go",
    )
    assert seen == {"db": 12, "binary": 11, "since": "2026-10-03T12:00:00",
                    "by": f"worktree land {CANON}", "bin": "/main/bin/endless-go"}


@pytest.mark.parametrize("up", [
    None,
    {"status": "current", "from": 12, "to": 12, "db": "/x/endless.db"},
])
def test_no_migration_clears_nothing(monkeypatch, up):
    def clear(*a, **kw):
        raise AssertionError("cleared without a migration")

    monkeypatch.setattr("endless.event_bridge.clear_land_schema_faults", clear)
    worktree_cmd._clear_land_schema_faults(up, "2026-10-03T12:00:00", CANON, None)


def test_a_failed_clear_never_fails_the_land(monkeypatch, capsys):
    def clear(*a, **kw):
        raise click.ClickException("store locked")

    monkeypatch.setattr("endless.event_bridge.clear_land_schema_faults", clear)
    worktree_cmd._clear_land_schema_faults(
        UP_MIGRATED, "2026-10-03T12:00:00", CANON, None,
    )
    err = capsys.readouterr().err
    assert "store locked" in err
    assert "endless errors list" in err
    assert "The land succeeded" in err
