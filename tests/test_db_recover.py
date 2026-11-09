"""`endless db recover` and the maintenance lock in the Python CLI. E-2397.

The recovery itself is Go's (internal/dbrecover, tested there against real
databases); these prove the Python half: `get_db()` honors the lock without
opening or creating anything, and `endless db recover` drives the Go command
and renders what it reports.
"""

from __future__ import annotations

import json
import os
import sqlite3
import subprocess
import time
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import cli, config, db


def _close_conftest_conn() -> None:
    if db._conn is not None:
        db._conn.close()
        db._conn = None


def _hold_lock(db_path: Path, pid: int) -> Path:
    lock = Path(f"{db_path}{db.MAINTENANCE_SUFFIX}")
    lock.write_text(json.dumps({"pid": pid, "command": "endless db recover"}))
    return lock


def _dead_pid() -> int:
    proc = subprocess.Popen(["true"])
    proc.wait()
    return proc.pid


def test_get_db_refuses_under_the_lock_without_creating_a_database(
        isolated_env, tmp_path, monkeypatch):
    _close_conftest_conn()
    path = Path(config.DB_PATH)
    for p in (path, Path(f"{path}-wal"), Path(f"{path}-shm")):
        if p.exists():
            p.unlink()
    _hold_lock(path, os.getppid())

    with pytest.raises(click.ClickException) as e:
        db.get_db()

    assert "under maintenance" in str(e.value)
    assert not path.exists()


def test_get_db_ignores_a_lock_whose_holder_has_exited(isolated_env):
    _close_conftest_conn()
    lock = _hold_lock(Path(config.DB_PATH), _dead_pid())
    try:
        assert db.get_db() is not None
    finally:
        lock.unlink()


def _ahead_with_linked_backup(path: Path) -> tuple[int, Path]:
    """Make the database one version ahead of endless-go, with the backup the
    'unwanted migration' took linked to it and a newer backup that carries it."""
    _close_conftest_conn()
    backups = path.parent / "backups"
    backups.mkdir(exist_ok=True)
    conn = sqlite3.connect(str(path))
    latest = conn.execute("SELECT max(version_id) FROM goose_db_version").fetchone()[0]
    linked = backups / "endless-20261018-051458.db"
    conn.execute("VACUUM INTO ?", (str(linked),))
    (backups / "pre-migration.jsonl").write_text(json.dumps({
        "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "backup": linked.name, "from": latest, "to": latest + 1,
    }) + "\n")
    conn.execute("INSERT INTO goose_db_version (version_id, is_applied) "
                 "VALUES (?, 1)", (latest + 1,))
    conn.commit()
    conn.execute("VACUUM INTO ?", (str(backups / "endless-20261018-060000.db"),))
    conn.close()
    return latest, linked


def _version(path: Path) -> int:
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        return conn.execute("SELECT max(version_id) FROM goose_db_version").fetchone()[0]
    finally:
        conn.close()


def test_cli_recover_says_there_is_nothing_to_recover(isolated_env):
    db.get_db()
    _close_conftest_conn()

    result = CliRunner().invoke(cli.main, ["db", "recover"])

    assert result.exit_code == 0, result.output
    assert "Nothing to recover" in result.output


def test_cli_recover_dry_run_names_both_versions_and_the_linked_backup(
        isolated_env):
    db.get_db()
    path = Path(config.DB_PATH)
    latest, linked = _ahead_with_linked_backup(path)

    result = CliRunner().invoke(cli.main, ["db", "recover", "--dry-run"])

    assert result.exit_code == 0, result.output
    assert f"schema version {latest + 1}, ahead of this endless (version {latest})" \
        in result.output
    assert linked.name in result.output
    assert "Dry run: no files were changed." in result.output
    assert _version(path) == latest + 1


def test_cli_recover_restores_the_pre_migration_backup(isolated_env):
    db.get_db()
    path = Path(config.DB_PATH)
    latest, _ = _ahead_with_linked_backup(path)

    result = CliRunner().invoke(cli.main, ["db", "recover", "--wait", "2"])

    assert result.exit_code == 0, result.output
    assert "Recovered" in result.output
    assert "integrity_check ok" in result.output
    assert _version(path) == latest
    assert not Path(f"{path}{db.MAINTENANCE_SUFFIX}").exists()
