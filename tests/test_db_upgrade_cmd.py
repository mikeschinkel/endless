"""E-2020: `endless db upgrade` — back up, bring the database forward, reseed.

The command is a thin CLI over `endless-go event upgrade`, which opens the
database FILE rather than through the application's connect, so it works while
every ordinary command is refusing the database. These pin the Python half: the
shell-out, and a report that names the backup and the versions.
"""

import subprocess
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import cli, event_bridge


def run_upgrade(monkeypatch, result: dict) -> str:
    monkeypatch.setattr("endless.event_bridge.upgrade_db", lambda: result)
    out = CliRunner().invoke(cli.main, ["db", "upgrade"])
    assert out.exit_code == 0, out.output
    return out.output


def test_upgrade_names_the_backup_and_both_versions(monkeypatch):
    backup = str(Path.home() / ".config/endless/backups/endless-20260930-120000.db")
    out = run_upgrade(monkeypatch, {
        "status": "migrated", "from": 9, "to": 10,
        "backup": backup, "backup_skipped": False,
    })
    assert "Backed up to: ~/.config/endless/backups/endless-20260930-120000.db" in out
    assert "from schema version 9 to 10" in out


def test_a_current_database_says_so(monkeypatch):
    out = run_upgrade(monkeypatch, {
        "status": "current", "from": 10, "to": 10,
        "backup": "/srv/b.db", "backup_skipped": True,
    })
    assert "Existing backup: /srv/b.db" in out
    assert "already at schema version 10" in out


def test_upgrade_shells_to_event_upgrade(monkeypatch):
    seen = {}

    class Result:
        returncode = 0
        stdout = '{"status": "current", "from": 10, "to": 10}'
        stderr = ""

    def run(cmd, **kw):
        seen["cmd"] = cmd
        return Result()

    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr(event_bridge, "_resolve_endless_go", lambda override=None: "/bin/eg")
    monkeypatch.setattr("endless.config.go_db_context_args", lambda: ["--db", "main"])
    monkeypatch.setattr(subprocess, "run", run)

    assert event_bridge.upgrade_db()["status"] == "current"
    assert seen["cmd"] == ["/bin/eg", "--db", "main", "event", "upgrade"]


def test_a_refusal_surfaces_the_go_error(monkeypatch):
    class Result:
        returncode = 1
        stdout = ""
        stderr = "endless-go event upgrade: refusing to open the main database"

    monkeypatch.setattr("endless.config.require_db_context", lambda: None)
    monkeypatch.setattr(event_bridge, "_resolve_endless_go", lambda override=None: "/bin/eg")
    monkeypatch.setattr("endless.config.go_db_context_args", lambda: [])
    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: Result())

    with pytest.raises(click.ClickException) as exc:
        event_bridge.upgrade_db()
    assert "refusing to open the main database" in exc.value.message
