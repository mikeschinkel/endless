"""Tests for E-2192: a self_dev land runs `endless-migrate up` before it records.

`worktree land` applied only the branch's `internal/schema/changes/` files. Its
goose migrations reached the real ledger only when an installed binary next
connected, so the worktree's endless-go that emits `task.landed` at Step 6 could
meet a database missing its own new column. E-2188 hit exactly that — `no such
column: focus_task_id` — and main was left advanced with the landing unrecorded.

What this module pins:

  1. Every self_dev land runs `up` at Step 5.5, after the ff-merge and before
     the record, behind the ONE backup the change loop already took.
  2. The order of `up` and the change files follows the task's own
     `.endless/tasks/e-<id>/land.toml` `[self_dev].schema_order`, read from the
     landing branch: migrations first by default, `changes-first` on request.
  3. Anything in land.toml the land does not understand refuses BEFORE the
     ff-merge — an invalid value, an unknown table or key, a top-level key,
     unreadable TOML — and names the offender.
  4. The recovery re-run — `just land` after "recording the landing failed" —
     runs `up` before retrying the record, so it no longer waits on an
     incidental migration by an installed binary.
  5. An `up` failure is surfaced like a change failure: main advanced, no
     record, re-run fixes it.
  6. `_migrate_up` threads the DB context as a flag and surfaces the
     executable's own error.
"""

import subprocess

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    SCHEMA_ORDER_CHANGES_FIRST,
    SCHEMA_ORDER_MIGRATIONS_FIRST,
    _migrate_up,
    _read_schema_order,
    land_worktree,
)

CANON = "E-2192"
CHANGE = "internal/schema/changes/e-2192-add-thing.sql"
LAND_TOML = ".endless/tasks/e-2192/land.toml"


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _head(repo, ref="HEAD"):
    return subprocess.run(
        ["git", "rev-parse", ref], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo plus a `feat` worktree, ready for a genuine land."""
    main = tmp_path / "main"
    main.mkdir()
    _git(["git", "init", "-q", "-b", "main"], main)
    _git(["git", "config", "user.email", "t@t.t"], main)
    _git(["git", "config", "user.name", "t"], main)
    _git(["git", "config", "commit.gpgsign", "false"], main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    return {"main": main, "worktree": worktree}


def _commit_on_feat(worktree, files):
    for rel, body in files.items():
        p = worktree / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "E-2192: branch work"], worktree)


def _patch_land(monkeypatch, main, worktree, calls, *, self_dev=True):
    """Mock the DB/registry boundary so land_worktree() runs on real git, and
    record every schema-touching step into `calls` with main's SHA at the time.
    """
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
        worktree_cmd, "_resolve_land_endless_go",
        lambda wt, root: "/wt/bin/endless-go" if self_dev else None,
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
    monkeypatch.setattr(
        worktree_cmd, "_rebuild_worktree_binary", lambda wt, canon: None
    )
    # E-2020, E-2205: Steps 5.2 and 5.6 build and swap the main checkout's
    # endless-go with `just`; a throwaway repo has no justfile. Their ordering
    # is pinned in test_worktree_land_record_binary.py.
    for name in ("_build_main_binary_next", "_swap_main_binary"):
        monkeypatch.setattr(worktree_cmd, name, lambda root, canon, base: None)

    def step(name, ret=None):
        def record(*args, **kwargs):
            calls.append((name, _head(main, "main")))
            return ret
        return record

    monkeypatch.setattr(
        worktree_cmd, "_build_migration_executable", step("build-migrate")
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_migrate_bin",
        lambda wt, root: "/wt/bin/endless-migrate",
    )
    monkeypatch.setattr("endless.event_bridge.backup_db", step("backup", {}))
    monkeypatch.setattr(
        worktree_cmd, "_migrate_up", step("up", {"status": "current"})
    )
    monkeypatch.setattr(worktree_cmd, "_migrate_change", step("apply", {}))
    monkeypatch.setattr(worktree_cmd, "_record_landing", step("record"))


def _names(calls):
    return [c[0] for c in calls]


# ---------------------------------------------------------------------------
# 1. every self_dev land runs `up`, after the merge and before the record
# ---------------------------------------------------------------------------

def test_up_runs_after_the_merge_and_before_the_record(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {"src/thing.py": "x = 1\n"})
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    feat_tip = _head(wt)
    land_worktree(CANON, dry_run=False)

    assert _names(calls) == ["build-migrate", "backup", "up", "record"]
    at = dict(calls)
    # Built while base is behind; `up` once base has advanced (E-1941's window).
    assert at["build-migrate"] != feat_tip
    assert at["up"] == feat_tip


def test_default_order_is_one_backup_then_up_then_changes(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {CHANGE: "-- ddl\n"})
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    land_worktree(CANON, dry_run=False)

    assert _names(calls) == ["build-migrate", "backup", "up", "apply", "record"]
    assert _names(calls).count("backup") == 1


def test_changes_first_order_from_land_toml(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {
        CHANGE: "-- ddl\n",
        LAND_TOML: '[self_dev]\nschema_order = "changes-first"\n',
    })
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    land_worktree(CANON, dry_run=False)

    assert _names(calls) == ["build-migrate", "backup", "apply", "up", "record"]
    assert _names(calls).count("backup") == 1


def test_non_self_dev_land_runs_no_up(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {CHANGE: "-- ddl\n"})
    calls = []
    _patch_land(monkeypatch, main, wt, calls, self_dev=False)

    land_worktree(CANON, dry_run=False)

    assert _names(calls) == ["record"]


# ---------------------------------------------------------------------------
# 2. land.toml: read from the branch, refused before the merge
# ---------------------------------------------------------------------------

def _write_land_toml(wt, body):
    p = wt / LAND_TOML
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(body)
    return p


def test_missing_land_toml_is_the_default(landable):
    assert _read_schema_order(landable["worktree"], CANON, True) == \
        SCHEMA_ORDER_MIGRATIONS_FIRST


def test_land_toml_without_self_dev_is_the_default(landable):
    wt = landable["worktree"]
    _write_land_toml(wt, "[self_dev]\n")
    assert _read_schema_order(wt, CANON, True) == SCHEMA_ORDER_MIGRATIONS_FIRST
    _write_land_toml(wt, "")
    assert _read_schema_order(wt, CANON, True) == SCHEMA_ORDER_MIGRATIONS_FIRST


def test_land_toml_names_both_orders(landable):
    wt = landable["worktree"]
    _write_land_toml(wt, '[self_dev]\nschema_order = "changes-first"\n')
    assert _read_schema_order(wt, CANON, True) == SCHEMA_ORDER_CHANGES_FIRST
    _write_land_toml(wt, '[self_dev]\nschema_order = "migrations-first"\n')
    assert _read_schema_order(wt, CANON, True) == SCHEMA_ORDER_MIGRATIONS_FIRST


@pytest.mark.parametrize("body, names", [
    ('[self_dev]\nschema_order = "sideways"\n',
     ["'sideways'", "migrations-first", "changes-first"]),
    ('[self_dev]\nschema_order = 1\n', ["migrations-first", "changes-first"]),
    ('[self_dev]\nschema_ordr = "changes-first"\n', ["schema_ordr", "[self_dev]"]),
    ('[merge]\nstrategy = "squash"\n', ["[merge]", "[self_dev]"]),
    ('schema_order = "changes-first"\n', ["schema_order", "top level", "[self_dev]"]),
    ('[self_dev\n', ["not readable TOML"]),
], ids=["bad-value", "non-string", "unknown-key", "unknown-table",
        "top-level-key", "bad-toml"])
def test_land_toml_refusals_name_the_file_and_the_offender(landable, body, names):
    wt = landable["worktree"]
    _write_land_toml(wt, body)
    with pytest.raises(click.ClickException) as ei:
        _read_schema_order(wt, CANON, True)
    msg = ei.value.message
    assert "land.toml" in msg
    assert "Nothing has been merged or migrated" in msg
    for name in names:
        assert name in msg


def test_invalid_land_toml_refuses_before_the_merge(landable, monkeypatch):
    """The refusal leaves base and the database untouched: no build, no backup,
    no migration, no record, and main where it was."""
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {
        CHANGE: "-- ddl\n",
        LAND_TOML: '[self_dev]\nschema_order = "sideways"\n',
    })
    calls = []
    _patch_land(monkeypatch, main, wt, calls)
    main_before = _head(main, "main")

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)

    assert calls == []
    assert _head(main, "main") == main_before


def test_land_toml_is_read_from_the_landing_branch_not_main(landable, monkeypatch):
    """The agent that wrote the change is the one who knows its order, so the
    branch's file wins; main carrying none does not reset it to the default."""
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {
        CHANGE: "-- ddl\n",
        LAND_TOML: '[self_dev]\nschema_order = "changes-first"\n',
    })
    assert not (main / LAND_TOML).exists()
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    land_worktree(CANON, dry_run=False)

    assert _names(calls).index("apply") < _names(calls).index("up")


# ---------------------------------------------------------------------------
# 3. the recovery re-run, and failure after the merge
# ---------------------------------------------------------------------------

def test_the_recovery_rerun_runs_up_before_recording(landable, monkeypatch):
    """E-2188's recovery path. The first land advances main and then fails to
    record; the re-run's ff-merge is a no-op and its change list is empty, but
    it still runs `up` before retrying the record."""
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {CHANGE: "-- ddl\n"})
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    attempts = []

    def record_fails_once(*args, **kwargs):
        attempts.append(1)
        calls.append(("record", _head(main, "main")))
        if len(attempts) == 1:
            raise click.ClickException("no such column: focus_task_id")

    monkeypatch.setattr(worktree_cmd, "_record_landing", record_fails_once)

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)
    assert _head(main, "main") == _head(wt)

    del calls[:]
    land_worktree(CANON, dry_run=False)

    assert _names(calls) == ["build-migrate", "backup", "up", "record"]
    assert len(attempts) == 2


def test_up_failure_reports_main_advanced_and_does_not_record(
    landable, monkeypatch
):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {CHANGE: "-- ddl\n"})
    calls = []
    _patch_land(monkeypatch, main, wt, calls)

    def up_fails(migrate_bin):
        raise click.ClickException("migrate up failed: disk I/O error")

    monkeypatch.setattr(worktree_cmd, "_migrate_up", up_fails)

    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert "main was advanced" in msg
    assert "disk I/O error" in msg
    assert "re-run" in msg
    assert "record" not in _names(calls)
    assert "apply" not in _names(calls)
    # No rollback of the merge.
    assert _head(main, "main") == _head(wt)


# ---------------------------------------------------------------------------
# 4. invoking the executable
# ---------------------------------------------------------------------------

def _stub_bin(tmp_path, body):
    b = tmp_path / "endless-migrate"
    b.write_text("#!/usr/bin/env bash\n" + body)
    b.chmod(0o755)
    return b


def test_migrate_up_threads_the_db_flag_and_parses_the_result(
    tmp_path, monkeypatch
):
    argv = tmp_path / "argv"
    binary = _stub_bin(
        tmp_path,
        f'printf "%s\\n" "$*" > {argv}\n'
        '''printf '{"status":"migrated","from":8,"to":10,"db":"/x.db"}\\n'\n''',
    )
    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr(
        "endless.config.migrate_db_context_args", lambda: ["--db", "main"]
    )

    res = _migrate_up(str(binary))

    assert argv.read_text().strip() == "--db main up"
    assert res == {"status": "migrated", "from": 8, "to": 10, "db": "/x.db"}


def test_migrate_up_surfaces_the_executables_own_error(tmp_path, monkeypatch):
    binary = _stub_bin(
        tmp_path,
        '''printf '{"status":"error","error":"no database at /x.db"}\\n'\n'''
        "exit 1\n",
    )
    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr("endless.config.migrate_db_context_args", lambda: [])

    with pytest.raises(click.ClickException) as ei:
        _migrate_up(str(binary))
    assert "no database at /x.db" in ei.value.message


# ---------------------------------------------------------------------------
# 5. PRODUCT: land.toml on a project that is not self_dev
# ---------------------------------------------------------------------------

def test_no_land_toml_changes_nothing_outside_self_dev(landable):
    assert _read_schema_order(landable["worktree"], CANON, False) == \
        SCHEMA_ORDER_MIGRATIONS_FIRST


@pytest.mark.parametrize("body", [
    '[self_dev]\nschema_order = "changes-first"\n',
    '[merge]\nstrategy = "squash"\n',
    'schema_order = "changes-first"\n',
], ids=["self-dev-table", "unknown-table", "top-level-key"])
def test_outside_self_dev_no_table_is_known_and_none_is_advertised(landable, body):
    """`[self_dev]` does nothing on someone else's project, so it is refused
    there like any unknown table — and no refusal teaches it to them."""
    wt = landable["worktree"]
    _write_land_toml(wt, body)
    with pytest.raises(click.ClickException) as ei:
        _read_schema_order(wt, CANON, False)
    msg = ei.value.message
    assert "no land settings apply to this project yet" in msg
    assert "the known tables are" not in msg


def test_a_bad_land_toml_refuses_a_non_self_dev_land_before_the_merge(
    landable, monkeypatch
):
    main, wt = landable["main"], landable["worktree"]
    _commit_on_feat(wt, {LAND_TOML: '[merge]\nstrategy = "squash"\n'})
    calls = []
    _patch_land(monkeypatch, main, wt, calls, self_dev=False)
    main_before = _head(main, "main")

    with pytest.raises(click.ClickException):
        land_worktree(CANON, dry_run=False)

    assert calls == []
    assert _head(main, "main") == main_before
