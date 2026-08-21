"""E-1942: `endless db restore` — recovering from a backup safely.

The failure this guards is specific. On 2026-08-10 a hand-rolled `cp` recovery
went wrong twice: once by copying over a database six processes still had open
(hot journal -> every reader got 'database is locked'), and once because
`endless db backup` writes a rollback-journal file via VACUUM INTO while the
live database is WAL. So the tests below care about holders, sidecars, journal
mode, and reversibility — not about the copy itself, which was never the part
that broke.
"""

import os
import shutil
import sqlite3
import subprocess
import sys
import time
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import cli, config, db, db_restore


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def make_db(path: Path, marker: str, *, wal: bool = False) -> Path:
    """A minimal but recognizable Endless database carrying `marker`."""
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(path))
    if wal:
        conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("CREATE TABLE tasks (id INTEGER PRIMARY KEY, title TEXT)")
    conn.execute("CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT)")
    conn.execute("INSERT INTO tasks (title) VALUES (?)", (marker,))
    conn.commit()
    conn.close()
    return path


def marker_of(path: Path) -> str:
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        return conn.execute("SELECT title FROM tasks").fetchone()[0]
    finally:
        conn.close()


def tables_of(path: Path) -> set[str]:
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        return {
            r[0] for r in conn.execute(
                "SELECT name FROM sqlite_master WHERE type='table'")
        }
    finally:
        conn.close()


@pytest.fixture
def target(isolated_env):
    """A clean target database path with the conftest connection closed.

    conftest opens `db.get_db()` against config.DB_PATH; leaving it open would
    have the test process itself holding the file a restore is about to move.
    """
    if db._conn is not None:
        db._conn.close()
        db._conn = None
    path = Path(config.DB_PATH)
    for p in db_restore.file_set(path):
        if p.exists():
            p.unlink()
    return path


@pytest.fixture
def no_holders(monkeypatch):
    """Nothing has the database open — the ordinary case, made deterministic."""
    monkeypatch.setattr(db_restore, "holders_of", lambda paths: [])


# ---------------------------------------------------------------------------
# journal mode, read from the header without taking a lock
# ---------------------------------------------------------------------------

def test_journal_mode_reads_wal_and_rollback_from_header(tmp_path):
    rollback = make_db(tmp_path / "rollback.db", "a")
    wal = make_db(tmp_path / "wal.db", "b", wal=True)
    assert db_restore.journal_mode_from_header(rollback) == "rollback"
    assert db_restore.journal_mode_from_header(wal) == "wal"


def test_journal_mode_classifies_non_databases(tmp_path):
    missing = tmp_path / "nope.db"
    empty = tmp_path / "empty.db"
    empty.touch()
    junk = tmp_path / "junk.db"
    junk.write_bytes(b"not a database at all, not even close")

    assert db_restore.journal_mode_from_header(missing) == "missing"
    assert db_restore.journal_mode_from_header(empty) == "empty"
    assert db_restore.journal_mode_from_header(junk) == "unknown"


def test_journal_mode_takes_no_lock_on_a_locked_database(tmp_path):
    """The whole point: the report still works when the DB is unreadable.

    A database held under an EXCLUSIVE transaction is exactly the state that
    made every reader fail during the incident; the header read must not care.
    """
    path = make_db(tmp_path / "locked.db", "a")
    holder = sqlite3.connect(str(path), timeout=0.1)
    holder.execute("BEGIN EXCLUSIVE")
    try:
        assert db_restore.journal_mode_from_header(path) == "rollback"
    finally:
        holder.rollback()
        holder.close()


# ---------------------------------------------------------------------------
# choosing a backup
# ---------------------------------------------------------------------------

def test_resolve_backup_defaults_to_the_newest(target):
    bdir = db_restore.backups_dir(target)
    make_db(bdir / "endless-20260810-010000.db", "old")
    newest = make_db(bdir / "endless-20260812-235959.db", "new")
    make_db(bdir / "endless-20260811-120000.db", "middle")

    assert db_restore.resolve_backup(target, None) == newest


def test_resolve_backup_accepts_a_path_or_a_bare_filename(target, tmp_path):
    bdir = db_restore.backups_dir(target)
    inside = make_db(bdir / "endless-20260810-010000.db", "inside")
    outside = make_db(tmp_path / "elsewhere" / "hand-made.db", "outside")

    assert db_restore.resolve_backup(target, "endless-20260810-010000.db") == inside
    assert db_restore.resolve_backup(target, str(outside)) == outside.resolve()


def test_resolve_backup_with_no_backups_names_the_directory(target):
    with pytest.raises(click.ClickException) as e:
        db_restore.resolve_backup(target, None)
    assert str(db_restore.backups_dir(target)) in str(e.value)


def test_resolve_backup_rejects_a_missing_name(target):
    with pytest.raises(click.ClickException) as e:
        db_restore.resolve_backup(target, "endless-19700101-000000.db")
    assert "not found" in str(e.value)


# ---------------------------------------------------------------------------
# validating the backup
# ---------------------------------------------------------------------------

def test_check_backup_accepts_a_sound_endless_database(tmp_path):
    check = db_restore.check_backup(make_db(tmp_path / "good.db", "a"))
    assert check.ok
    assert check.integrity == "ok"


@pytest.mark.parametrize("kind", ["missing", "empty", "junk", "truncated"])
def test_check_backup_refuses_unusable_files(tmp_path, kind):
    path = tmp_path / f"{kind}.db"
    if kind == "empty":
        path.touch()
    elif kind == "junk":
        path.write_bytes(b"definitely not sqlite")
    elif kind == "truncated":
        path.write_bytes(db_restore.SQLITE_MAGIC + b"\x00" * 100)
    check = db_restore.check_backup(path)
    assert not check.ok
    assert check.problem


def test_check_backup_refuses_a_foreign_sqlite_database(tmp_path):
    """A SQLite file in the backups directory is not automatically a database."""
    path = tmp_path / "someone-elses.db"
    conn = sqlite3.connect(str(path))
    conn.execute("CREATE TABLE unrelated (x INTEGER)")
    conn.commit()
    conn.close()

    check = db_restore.check_backup(path)
    assert not check.ok
    assert "not an Endless database" in check.problem


def test_check_backup_does_not_modify_the_backup(tmp_path):
    """Validation opens read-only: the one file standing between the user and
    data loss must not be rewritten by the act of inspecting it."""
    path = make_db(tmp_path / "good.db", "a")
    before = path.read_bytes()

    assert db_restore.check_backup(path).ok

    assert path.read_bytes() == before
    assert not Path(str(path) + "-wal").exists()
    assert not Path(str(path) + "-shm").exists()


# ---------------------------------------------------------------------------
# holders
# ---------------------------------------------------------------------------

def can_enumerate_holders() -> bool:
    return shutil.which("lsof") is not None or Path("/proc").is_dir()


def test_holders_of_finds_an_open_handle_by_pid_and_command(target):
    """An open handle on the DB is reported, with the command that holds it."""
    make_db(target, "live")
    script = (
        "import sqlite3,sys,time\n"
        "c=sqlite3.connect(sys.argv[1])\n"
        "c.execute('SELECT count(*) FROM tasks').fetchone()\n"
        "print('ready', flush=True)\n"
        "time.sleep(30)\n"
    )
    proc = subprocess.Popen([sys.executable, "-c", script, str(target)],
                            stdout=subprocess.PIPE, text=True)
    try:
        assert proc.stdout.readline().strip() == "ready"
        deadline = time.time() + 10
        holders = []
        while time.time() < deadline:
            holders = db_restore.holders_of(db_restore.file_set(target)) or []
            if any(h.pid == proc.pid for h in holders):
                break
            time.sleep(0.2)
        if not holders and not can_enumerate_holders():
            pytest.skip("no lsof and no /proc: holders cannot be enumerated here")
        match = [h for h in holders if h.pid == proc.pid]
        assert match, f"pid {proc.pid} not among {holders}"
        assert "sqlite3" in match[0].command or "python" in match[0].command.lower()
    finally:
        proc.kill()
        proc.wait()


def test_holders_of_excludes_this_process(target):
    """The restoring process must not report itself as a reason to refuse."""
    make_db(target, "live")
    conn = sqlite3.connect(str(target))
    conn.execute("SELECT count(*) FROM tasks").fetchone()
    try:
        holders = db_restore.holders_of(db_restore.file_set(target))
        assert holders is None or all(h.pid != os.getpid() for h in holders)
    finally:
        conn.close()


# ---------------------------------------------------------------------------
# refusal
# ---------------------------------------------------------------------------

def test_restore_refuses_while_the_database_is_open(target, monkeypatch):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(
        db_restore, "holders_of",
        lambda paths: [db_restore.Holder(4321, "endless task show -p E-1898")],
    )

    with pytest.raises(click.ClickException) as e:
        db_restore.run_restore(None, dry_run=False, force=False)

    assert "refusing to restore" in str(e.value)
    assert "--force" in str(e.value)
    assert marker_of(target) == "live"
    assert not db_restore.pre_restore_dir(target).exists()


def test_restore_refuses_when_holders_cannot_be_determined(target, monkeypatch):
    """`None` means 'could not ask', which is the unsafe case, not an all-clear."""
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(db_restore, "holders_of", lambda paths: None)

    with pytest.raises(click.ClickException) as e:
        db_restore.run_restore(None, dry_run=False, force=False)

    assert "could not determine" in str(e.value)
    assert marker_of(target) == "live"


def test_restore_refuses_a_bad_backup_before_touching_anything(target, no_holders):
    make_db(target, "live")
    bad = db_restore.backups_dir(target) / "endless-20260810-010000.db"
    bad.parent.mkdir(parents=True, exist_ok=True)
    bad.write_bytes(b"not sqlite")

    with pytest.raises(click.ClickException) as e:
        db_restore.run_restore(None, dry_run=False, force=False)

    assert "refusing to restore" in str(e.value)
    assert marker_of(target) == "live"


def test_force_restores_despite_holders(target, monkeypatch):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(
        db_restore, "holders_of",
        lambda paths: [db_restore.Holder(4321, "endless task show -p E-1898")],
    )

    db_restore.run_restore(None, dry_run=False, force=True)

    assert marker_of(target) == "backup"


# ---------------------------------------------------------------------------
# the restore
# ---------------------------------------------------------------------------

def test_restore_replaces_the_database_and_re_establishes_wal(target, no_holders):
    """VACUUM INTO backups are rollback-journal; the live DB must come back WAL."""
    make_db(target, "live", wal=True)
    backup = make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db",
                     "backup")
    assert db_restore.journal_mode_from_header(backup) == "rollback"

    db_restore.run_restore(None, dry_run=False, force=False)

    assert marker_of(target) == "backup"
    assert db_restore.journal_mode_from_header(target) == "wal"


def test_restore_keeps_the_pre_restore_database_aside(target, no_holders):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")

    db_restore.run_restore(None, dry_run=False, force=False)

    parked = list(db_restore.pre_restore_dir(target).glob("endless-*.db"))
    assert len(parked) == 1
    assert marker_of(parked[0]) == "live"


def test_pre_restore_files_stay_out_of_the_rotated_backups_directory(target,
                                                                    no_holders):
    """monitor.BackupDB rotates backups/ by name, keeping the last 60. Parking
    the pre-restore copy there would quietly evict real backups."""
    bdir = db_restore.backups_dir(target)
    make_db(target, "live")
    make_db(bdir / "endless-20260810-010000.db", "backup")
    before = sorted(p.name for p in bdir.iterdir())

    db_restore.run_restore(None, dry_run=False, force=False)

    assert sorted(p.name for p in bdir.iterdir()) == before
    assert db_restore.pre_restore_dir(target) != bdir


def test_restore_moves_stale_sidecars_aside(target, no_holders):
    """A leftover -journal beside a fresh database is failure 1 in a bottle."""
    make_db(target, "live")
    hot = Path(str(target) + "-journal")
    hot.write_bytes(b"hot journal left by a killed writer")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")

    db_restore.run_restore(None, dry_run=False, force=False)

    assert not hot.exists()
    parked = db_restore.pre_restore_dir(target)
    assert any(p.name.endswith(".db-journal") for p in parked.iterdir())


def test_restore_works_when_the_target_is_gone(target, no_holders):
    """Recovering a deleted database is a legitimate reason to be here."""
    assert not target.exists()
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")

    db_restore.run_restore(None, dry_run=False, force=False)

    assert marker_of(target) == "backup"
    assert not db_restore.pre_restore_dir(target).exists()


def test_restore_does_not_migrate_the_restored_database(target, no_holders):
    """No path here goes through db.get_db(), which applies schema on connect.

    A restore that silently migrated would defeat the point: the reason to
    restore is usually that a migration ran when it should not have.
    """
    make_db(target, "live")
    backup = make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db",
                     "backup")
    expected = tables_of(backup)

    db_restore.run_restore(None, dry_run=False, force=False)

    assert tables_of(target) == expected


def test_failed_verification_names_the_parked_copy(target, no_holders,
                                                   monkeypatch):
    """integrity_check != ok must fail loudly, and say how to undo the restore."""
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(db_restore, "_reestablish_wal",
                        lambda path: ("wal", "*** in database main ***"))

    with pytest.raises(click.ClickException) as e:
        db_restore.run_restore(None, dry_run=False, force=False)

    parked = list(db_restore.pre_restore_dir(target).glob("endless-*.db"))
    assert len(parked) == 1
    assert str(parked[0]) in str(e.value)
    assert "integrity_check" in str(e.value)


def test_a_failed_copy_names_the_parked_copy_instead_of_raising_oserror(
        target, no_holders, monkeypatch):
    """Disk full mid-restore is a plausible 5am outcome; a traceback is not the
    answer when the parked copy's path is what the user needs."""
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")

    def boom(*args, **kwargs):
        raise OSError(28, "No space left on device")

    monkeypatch.setattr(db_restore, "_copy_into_place", boom)

    with pytest.raises(click.ClickException) as e:
        db_restore.run_restore(None, dry_run=False, force=False)

    parked = list(db_restore.pre_restore_dir(target).glob("endless-*.db"))
    assert len(parked) == 1
    assert str(parked[0]) in str(e.value)
    assert marker_of(parked[0]) == "live"


# ---------------------------------------------------------------------------
# dry run
# ---------------------------------------------------------------------------

def test_dry_run_reports_holders_and_journal_modes_and_changes_nothing(
        target, monkeypatch, capsys):
    make_db(target, "live", wal=True)
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(
        db_restore, "holders_of",
        lambda paths: [db_restore.Holder(4321, "endless task show -p E-1898")],
    )

    db_restore.run_restore(None, dry_run=True, force=False)

    out = capsys.readouterr().out
    assert "dry run" in out
    assert "pid 4321" in out
    assert "endless task show -p E-1898" in out
    assert "journal: wal" in out       # the target
    assert "journal: rollback" in out  # the VACUUM INTO backup
    assert "integrity: ok" in out
    assert marker_of(target) == "live"
    assert not db_restore.pre_restore_dir(target).exists()


def test_dry_run_reports_unknown_holders_honestly(target, monkeypatch, capsys):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(db_restore, "holders_of", lambda paths: None)

    db_restore.run_restore(None, dry_run=True, force=False)

    assert "UNKNOWN" in capsys.readouterr().out


# ---------------------------------------------------------------------------
# CLI wiring
# ---------------------------------------------------------------------------

def test_cli_db_restore_dry_run(target, monkeypatch):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(db_restore, "holders_of", lambda paths: [])

    result = CliRunner().invoke(cli.main, ["db", "restore", "--dry-run"])

    assert result.exit_code == 0, result.output
    assert "Dry run: no files were changed." in result.output
    assert marker_of(target) == "live"


def test_cli_db_restore_refusal_exits_nonzero(target, monkeypatch):
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(
        db_restore, "holders_of",
        lambda paths: [db_restore.Holder(4321, "endless-go session-status --monitor")],
    )

    result = CliRunner().invoke(cli.main, ["db", "restore"])

    assert result.exit_code != 0
    assert "refusing to restore" in result.output
    assert marker_of(target) == "live"


def test_cli_db_restore_enforces_the_worktree_db_gate(target, monkeypatch):
    """The gate normally fires at db.get_db(); restore reaches neither it nor a
    Go shellout, so it must call require_db_context itself."""
    make_db(target, "live")
    make_db(db_restore.backups_dir(target) / "endless-20260810-010000.db", "backup")
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    monkeypatch.setattr(config, "gated_worktree_root", lambda cwd=None: Path("/proj"))

    result = CliRunner().invoke(cli.main, ["db", "restore"])

    assert result.exit_code != 0
    assert "--db" in result.output
    assert marker_of(target) == "live"
