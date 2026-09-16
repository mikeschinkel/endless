"""Tests for E-2088: a self_dev land migrates with the migration-only executable.

ED-1571. Under ED-1567 a CANDIDATE binary may never migrate the real ledger, and
E-1664 made handing the land the WORKTREE's own endless-go an invariant rather
than a choice — it is the only binary whose embedded schema and enums match the
rows the land just wrote. That binary is a candidate by definition (its path
carries the worktree marker), so the invariant and the prohibition point opposite
ways, and once E-2020 lands a self_dev land has no binary permitted to apply its
own migration: the installed one does not carry it, the worktree one may not run
it.

The way out is a third binary. `worktree land` builds
<worktree>/bin/endless-migrate from the landing branch and applies the branch's
changes with that — an executable carrying the migration set and no application
at all, so it has no expectation of the database it is about to change and
nothing the database can disappoint.

What this module pins, and nothing else does:

  1. The build is CONDITIONAL. A land carrying no schema change builds no
     executable and invokes none. Building a tool in order not to use it would
     make its absence untestable.
  2. The build precedes the ff-merge and the INVOKE follows it. E-1941's window
     is unchanged for the apply; the build is earlier for E-1941's own reason —
     a tree that cannot compile its migration tool must abort while base and the
     database are untouched.
  3. Scope is self_dev. A non-self_dev land builds nothing, resolves nothing and
     applies nothing: `internal/schema/changes/` is endless's own schema, and a
     downstream project has one installed binary that applies its own migrations
     under ED-1570.
  4. The two binaries stay separated. The migration executable applies; the
     worktree's endless-go backs up and records. That pairing is the half of
     E-1664 that survives E-2088 and nothing else covers it.
  5. `_migrate_change` invokes the executable the way every other Go shellout is
     invoked — `--config-dir` as a per-invocation flag (E-1429; endless-migrate
     keeps that flag where endless-go moved to --db, see
     config.migrate_db_context_args) — and surfaces
     the executable's own error text rather than a generic one.

The ordering of the whole land, and the post-merge failure surfacing, live in
tests/test_worktree_land_schema_apply.py with E-1941's other ordering
assertions; they exercise `_migrate_change` there.
"""

import os
import subprocess

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    _build_migration_executable,
    _migrate_change,
    _resolve_land_migrate_bin,
    land_worktree,
)

CANON = "E-2088"
CHANGE = "internal/schema/changes/e-2088-add-thing.sql"


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


def _fake_just(tmp_path, exit_code=0):
    """A `just` stub on PATH that records every recipe it was asked for."""
    d = tmp_path / "stubs"
    d.mkdir(exist_ok=True)
    log = tmp_path / "just.log"
    (d / "just").write_text(
        "#!/usr/bin/env bash\n"
        f'printf "%s\\n" "$*" >> {log}\n'
        f"exit {exit_code}\n"
    )
    (d / "just").chmod(0o755)
    return d, log


def _make_migrate_bin(worktree, body="exit 0\n"):
    """An executable standing in for the built <worktree>/bin/endless-migrate."""
    bin_dir = worktree / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    binary = bin_dir / "endless-migrate"
    binary.write_text("#!/usr/bin/env bash\n" + body)
    binary.chmod(0o755)
    return binary


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
    monkeypatch.setattr(
        worktree_cmd, "_rebuild_worktree_binary", lambda wt, canon: None
    )
    monkeypatch.setattr(
        "endless.event_bridge.backup_db", lambda endless_go_bin=None: {}
    )


def _noop_record(item_id, proj_name, branch, base_branch, canonical,
                 merge_sha, endless_go_bin=None):
    return None


def _commit_change_on_feat(worktree, rel=CHANGE):
    p = worktree / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text("-- ddl\n")
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "E-2088: add schema change"], worktree)


# ---------------------------------------------------------------------------
# 1. resolving the executable
# ---------------------------------------------------------------------------

def test_resolves_the_worktree_migrate_binary(landable, monkeypatch):
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    binary = _make_migrate_bin(wt)
    assert _resolve_land_migrate_bin(wt, landable["main"]) == str(binary)


def test_missing_migrate_binary_fails_loudly(landable, monkeypatch):
    """Never slide to something else. The land built this binary a step ago, so
    an absent one means the build silently did not happen (E-1662's rule applied
    to the second binary)."""
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    with pytest.raises(click.ClickException) as ei:
        _resolve_land_migrate_bin(wt, landable["main"])
    assert "endless-migrate" in ei.value.message
    assert "just migrate-bin" in ei.value.message


def test_non_executable_migrate_binary_fails_loudly(landable, monkeypatch):
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    binary = _make_migrate_bin(wt)
    binary.chmod(0o644)
    with pytest.raises(click.ClickException):
        _resolve_land_migrate_bin(wt, landable["main"])


def test_non_self_dev_resolves_no_migrate_binary(landable, monkeypatch):
    """A downstream project has one installed binary and applies its own
    migrations under ED-1570. No such executable exists there."""
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: False)
    _make_migrate_bin(wt)
    assert _resolve_land_migrate_bin(wt, landable["main"]) is None


# ---------------------------------------------------------------------------
# 2. building it
# ---------------------------------------------------------------------------

def test_build_runs_just_migrate_bin_in_the_worktree(landable, monkeypatch, tmp_path):
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    d, log = _fake_just(tmp_path)
    monkeypatch.setenv("PATH", f"{d}:{os.environ['PATH']}")
    _build_migration_executable(wt, CANON)
    assert log.read_text().strip() == "migrate-bin"


def test_build_failure_aborts_before_anything_advances(
    landable, monkeypatch, tmp_path
):
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: True)
    d, _ = _fake_just(tmp_path, exit_code=1)
    monkeypatch.setenv("PATH", f"{d}:{os.environ['PATH']}")
    with pytest.raises(click.ClickException) as ei:
        _build_migration_executable(wt, CANON)
    assert "Nothing has been merged or migrated" in ei.value.message


def test_build_skipped_for_non_self_dev(landable, monkeypatch, tmp_path):
    wt = landable["worktree"]
    monkeypatch.setattr("endless.config.project_is_self_dev", lambda root: False)
    d, log = _fake_just(tmp_path)
    monkeypatch.setenv("PATH", f"{d}:{os.environ['PATH']}")
    _build_migration_executable(wt, CANON)
    assert not log.exists()


# ---------------------------------------------------------------------------
# 3. invoking it
# ---------------------------------------------------------------------------

def test_invocation_threads_config_dir_and_the_change_path(
    landable, monkeypatch, tmp_path
):
    """The DB target rides as a per-invocation flag (E-1429), never an exported
    variable: an export would silently redirect every later migration."""
    wt = landable["worktree"]
    argv_log = tmp_path / "argv.log"
    binary = _make_migrate_bin(
        wt,
        f'printf "%s\\n" "$*" > {argv_log}\n'
        '''printf '{"name":"e-2088-add-thing","status":"applied","db":"/tmp/x.db"}\\n'\n'''
        "exit 0\n",
    )
    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    # E-1668 split the two binaries' spellings: endless-go takes
    # --db main|sandbox, endless-migrate keeps --config-dir. Stubbing the WRONG
    # one here would let this test pass while a land threaded a flag the
    # executable cannot parse.
    monkeypatch.setattr(
        "endless.config.migrate_db_context_args", lambda: ["--config-dir", "/cfg"]
    )

    res = _migrate_change(str(binary), wt / CHANGE)

    assert argv_log.read_text().strip() == f"--config-dir /cfg apply {wt / CHANGE}"
    assert res["status"] == "applied"
    assert res["name"] == "e-2088-add-thing"


def test_invocation_surfaces_the_executables_own_error(
    landable, monkeypatch, tmp_path
):
    wt = landable["worktree"]
    binary = _make_migrate_bin(
        wt,
        '''printf '{"status":"error","error":"no such table: thing"}\\n'\n'''
        "exit 1\n",
    )
    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr("endless.config.go_db_context_args", lambda: [])

    with pytest.raises(click.ClickException) as ei:
        _migrate_change(str(binary), wt / CHANGE)
    assert "no such table: thing" in ei.value.message


def test_invocation_falls_back_to_stderr_when_there_is_no_json(
    landable, monkeypatch, tmp_path
):
    """A binary that died before it could print a document still has to say
    something useful — a bare 'it failed' is the message that teaches nothing."""
    wt = landable["worktree"]
    binary = _make_migrate_bin(wt, 'echo "segfault" >&2\nexit 1\n')
    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr("endless.config.go_db_context_args", lambda: [])

    with pytest.raises(click.ClickException) as ei:
        _migrate_change(str(binary), wt / CHANGE)
    assert "segfault" in ei.value.message


# ---------------------------------------------------------------------------
# 4. the land: conditional build, ordering, and the two-binary split
# ---------------------------------------------------------------------------

def test_land_builds_and_invokes_it_around_the_ff_merge(landable, monkeypatch):
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt)

    calls = []
    main_at = {}

    def fake_build(wt_, canon):
        calls.append("build-migrate")
        main_at["build"] = _head(main, "main")

    def fake_resolve(wt_, root):
        calls.append("resolve-migrate")
        return "/bin/echo"

    def fake_apply(migrate_bin, change_path):
        calls.append("apply")
        main_at["apply"] = _head(main, "main")
        main_at["bin"] = migrate_bin
        return {}

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        calls.append("record")
        main_at["record_bin"] = endless_go_bin

    monkeypatch.setattr(worktree_cmd, "_build_migration_executable", fake_build)
    monkeypatch.setattr(worktree_cmd, "_resolve_land_migrate_bin", fake_resolve)
    monkeypatch.setattr(worktree_cmd, "_migrate_change", fake_apply)
    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)

    feat_tip = _head(wt)
    land_worktree(CANON, dry_run=False)

    assert calls == ["build-migrate", "resolve-migrate", "apply", "record"]
    # Built while base is still behind: a broken build aborts having touched
    # neither base nor the database.
    assert main_at["build"] != feat_tip
    # Invoked once base HAS advanced — E-1941's window, unchanged.
    assert main_at["apply"] == feat_tip


def test_the_migration_executable_applies_and_endless_go_records(
    landable, monkeypatch
):
    """The half of E-1664 that survives E-2088. Two binaries, one database: the
    migration executable applies the change, then the worktree's endless-go —
    whose embedded enums match the rows just inserted — records the landing."""
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt)

    seen = {}
    monkeypatch.setattr(
        worktree_cmd, "_build_migration_executable", lambda wt_, canon: None
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_migrate_bin",
        lambda wt_, root: str(wt / "bin" / "endless-migrate"),
    )

    def fake_apply(migrate_bin, change_path):
        seen["apply_bin"] = migrate_bin
        return {}

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        seen["record_bin"] = endless_go_bin

    monkeypatch.setattr(worktree_cmd, "_migrate_change", fake_apply)
    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)

    land_worktree(CANON, dry_run=False)

    assert seen["apply_bin"].endswith("/bin/endless-migrate")
    assert seen["record_bin"] == "/bin/echo"
    assert seen["apply_bin"] != seen["record_bin"]


def test_a_land_with_no_schema_change_builds_nothing(landable, monkeypatch):
    """Assertion 4. Nothing to apply means nothing to build — and if the build
    were unconditional, its absence could never be asserted."""
    main, wt = landable["main"], landable["worktree"]
    (wt / "README").write_text("edited\n")
    _git(["git", "add", "-A"], wt)
    _git(["git", "commit", "-q", "-m", "E-2088: no schema change"], wt)
    _patch_land(monkeypatch, main, wt)

    calls = []
    monkeypatch.setattr(
        worktree_cmd, "_build_migration_executable",
        lambda wt_, canon: calls.append("build-migrate"),
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_migrate_bin",
        lambda wt_, root: calls.append("resolve-migrate"),
    )
    monkeypatch.setattr(
        worktree_cmd, "_migrate_change",
        lambda migrate_bin, change_path: calls.append("apply"),
    )
    monkeypatch.setattr(worktree_cmd, "_record_landing", _noop_record)

    land_worktree(CANON, dry_run=False)

    assert calls == []
    assert _head(main, "main") == _head(wt)


def test_a_non_self_dev_land_builds_nothing_and_applies_nothing(
    landable, monkeypatch
):
    """Assertion 5, as far as a land can assert it. A downstream project has no
    land-time executable; its one installed binary applies its own migrations
    under ED-1570 through `endless-go event apply-change`."""
    main, wt = landable["main"], landable["worktree"]
    _commit_change_on_feat(wt)
    _patch_land(monkeypatch, main, wt, self_dev=False)
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_endless_go", lambda w, r: None
    )

    calls = []
    monkeypatch.setattr(
        worktree_cmd, "_build_migration_executable",
        lambda wt_, canon: calls.append("build-migrate"),
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_migrate_bin",
        lambda wt_, root: calls.append("resolve-migrate"),
    )
    monkeypatch.setattr(
        worktree_cmd, "_migrate_change",
        lambda migrate_bin, change_path: calls.append("apply"),
    )
    monkeypatch.setattr(worktree_cmd, "_record_landing", _noop_record)

    land_worktree(CANON, dry_run=False)

    assert calls == []
