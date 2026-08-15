"""E-1942: `endless db backup` names the file it wrote.

"Database backed up." is unusable as the first half of a restore — the point of
a backup is being able to say which one. The message now carries the path, with
$HOME collapsed to ~ so it is readable and still paste-able.
"""

from pathlib import Path

from click.testing import CliRunner

from endless import cli, config


def run_backup(monkeypatch, result: dict) -> str:
    """Invoke `endless db backup` with the Go shellout stubbed to `result`."""
    monkeypatch.setattr("endless.event_bridge.backup_db",
                        lambda endless_go_bin=None: result)
    # `db backup` is an always-main operation and pins main_config_dir(); the
    # test asserts on the message, not on which DB was hit, so leave it be.
    out = CliRunner().invoke(cli.main, ["db", "backup"])
    assert out.exit_code == 0, out.output
    return out.output


def test_backup_names_the_file_it_wrote(monkeypatch):
    path = str(Path.home() / ".config/endless/backups/endless-20260815-055020.db")
    out = run_backup(monkeypatch, {"status": "ok", "path": path})

    assert "Database backed up to " in out
    assert "~/.config/endless/backups/endless-20260815-055020.db" in out
    assert str(Path.home()) not in out


def test_backup_outside_home_prints_the_absolute_path(monkeypatch):
    out = run_backup(monkeypatch, {"status": "ok", "path": "/srv/endless/b.db"})
    assert "/srv/endless/b.db" in out


def test_throttled_backup_does_not_claim_to_have_written_one(monkeypatch):
    """Inside the 60s window nothing is written; saying "backed up to X" would
    date the user's backup wrong by up to a minute at exactly the wrong moment."""
    path = str(Path.home() / ".config/endless/backups/endless-20260815-055020.db")
    out = run_backup(monkeypatch, {"status": "skipped", "path": path})

    assert "nothing written" in out
    assert "Database backed up to" not in out
    assert "~/.config/endless/backups/endless-20260815-055020.db" in out


def test_a_pre_e1942_binary_falls_back_to_the_old_message(monkeypatch):
    """A self-dev worktree runs its own Python against the INSTALLED endless-go,
    which may predate this change and report no path. Say less, not wrong."""
    out = run_backup(monkeypatch, {"status": "ok"})
    assert out.strip() == "Database backed up."


# ---------------------------------------------------------------------------
# config.tilde
# ---------------------------------------------------------------------------

def test_tilde_collapses_a_leading_home():
    assert config.tilde(Path.home() / "a" / "b") == "~/a/b"


def test_tilde_leaves_other_paths_alone():
    assert config.tilde("/var/tmp/x") == "/var/tmp/x"
    assert config.tilde(Path("/etc/hosts")) == "/etc/hosts"


def test_tilde_only_collapses_the_leading_occurrence():
    """A path is shown so it can be pasted; rewriting a home-shaped segment in
    the middle would break that."""
    home = str(Path.home())
    assert config.tilde(f"{home}/x{home}/y") == f"~/x{home}/y"


def test_worktree_cmd_tilde_delegates():
    """One implementation, not two (worktree_cmd had its own copy)."""
    from endless import worktree_cmd
    p = Path.home() / "Projects" / "endless"
    assert worktree_cmd._tilde(p) == config.tilde(p) == "~/Projects/endless"
