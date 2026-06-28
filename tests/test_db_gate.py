"""Tests for the explicit --db gate inside self-dev worktrees (E-1429/E-1476)."""

import click
import pytest
from click.testing import CliRunner

from endless import config
from endless.cli import main


def _make_worktree(tmp_path, sandbox: bool, task_id: str = "555"):
    """Build <tmp>/proj/.endless/{config.json, worktrees/e-<id>} and
    return the worktree dir. config.json sets self_dev to `sandbox`."""
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    wt = endless / "worktrees" / f"e-{task_id}"
    wt.mkdir(parents=True)
    (endless / "config.json").write_text(
        '{"self_dev": %s}\n' % ("true" if sandbox else "false")
    )
    return wt


# --- resolution helpers --------------------------------------------------


def test_worktree_dir_name_detects_segment():
    pathlib = __import__("pathlib")
    assert config.worktree_dir_name(
        pathlib.Path("/x/proj/.endless/worktrees/e-1429/internal")
    ) == "e-1429"
    assert config.worktree_dir_name(
        pathlib.Path("/x/proj/.endless/worktrees/e-77")
    ) == "e-77"
    # ED-1515: a named-alternate dir is not recognized as a task worktree.
    assert config.worktree_dir_name(
        pathlib.Path("/x/proj/.endless/worktrees/e-77-some-slug")
    ) is None
    assert config.worktree_dir_name(pathlib.Path("/x/proj")) is None


def test_gated_worktree_root_requires_sandbox_flag(tmp_path, monkeypatch):
    wt_on = _make_worktree(tmp_path, sandbox=True, task_id="1")
    monkeypatch.chdir(wt_on)
    assert config.gated_worktree_root() == wt_on.parent.parent.parent

    wt_off = _make_worktree(tmp_path, sandbox=False, task_id="2")
    monkeypatch.chdir(wt_off)
    assert config.gated_worktree_root() is None


def test_apply_db_choice_main(monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.apply_db_choice("main")
    assert config.RESOLVED_CONFIG_DIR == config.main_config_dir()
    assert str(config.DB_PATH).endswith("/.config/endless/endless.db")


def test_apply_db_choice_sandbox(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="909")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.apply_db_choice("sandbox")
    assert config.RESOLVED_CONFIG_DIR == config.sandbox_config_dir("e-909")
    assert config.RESOLVED_CONFIG_DIR.name == "endless"
    assert "sandboxes/e-909/endless" in str(config.RESOLVED_CONFIG_DIR)


def test_apply_db_choice_sandbox_outside_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)  # not a worktree
    with pytest.raises(ValueError):
        config.apply_db_choice("sandbox")


def test_apply_db_choice_unknown_value(monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    with pytest.raises(ValueError, match="unknown --db value"):
        config.apply_db_choice("worktree")  # the old value is no longer accepted


# --- always-main default for land-flow commands (E-1628) -----------------


def test_default_db_to_main_pins_main_when_unset(tmp_path, monkeypatch):
    """From a sandbox-routed worktree with no explicit --db, an always-main
    operation pins the real DB so reads and downstream endless-go shellouts hit
    main, not the sandbox."""
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.default_db_to_main()
    assert config.RESOLVED_CONFIG_DIR == config.main_config_dir()
    assert config.go_db_context_args() == [
        "--config-dir",
        str(config.main_config_dir()),
    ]


def test_default_db_to_main_honors_explicit_sandbox(tmp_path, monkeypatch):
    """An explicit --db sandbox (RESOLVED_CONFIG_DIR already set) is NOT
    overridden by the always-main default."""
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    sandbox = config.sandbox_config_dir("e-1628")
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", sandbox)
    config.default_db_to_main()
    assert config.RESOLVED_CONFIG_DIR == sandbox  # unchanged


def test_land_worktree_pins_main_from_sandbox(tmp_path, monkeypatch):
    """`endless worktree land` (no --db) pins main before doing any work, even
    when cwd routing would otherwise resolve to the sandbox. Reproduces the
    E-1542 mis-target: without the pin the task.landed emit hits the sandbox."""
    from endless import worktree_cmd

    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)

    seen = {}

    def _spy_project_root():
        # Runs immediately after land_worktree's default_db_to_main(); capture
        # the pinned context, then short-circuit the heavy git work.
        seen["resolved"] = config.RESOLVED_CONFIG_DIR
        raise click.ClickException("stop after pin")

    monkeypatch.setattr(worktree_cmd, "_project_root", _spy_project_root)
    result = CliRunner().invoke(main, ["worktree", "land", "E-1628"])
    assert result.exit_code != 0  # stopped by the spy
    assert seen["resolved"] == config.main_config_dir()


def test_db_apply_change_pins_main_from_sandbox(tmp_path, monkeypatch):
    """`endless db apply-change` (no --db) pins main so the _schema_version
    marker lands in the real DB, not the sandbox."""
    from endless import event_bridge

    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)

    change_file = tmp_path / "0001-change.sql"
    change_file.write_text("-- noop\n")

    seen = {}

    def _spy_apply_change(path):
        seen["resolved"] = config.RESOLVED_CONFIG_DIR
        return {"name": "0001-change", "status": "applied"}

    monkeypatch.setattr(event_bridge, "apply_change", _spy_apply_change)
    result = CliRunner().invoke(main, ["db", "apply-change", str(change_file)])
    assert result.exit_code == 0, result.output
    assert seen["resolved"] == config.main_config_dir()


def test_db_apply_change_honors_explicit_sandbox(tmp_path, monkeypatch):
    """An explicit --db sandbox is not overridden by the always-main default."""
    from endless import event_bridge

    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)

    change_file = tmp_path / "0001-change.sql"
    change_file.write_text("-- noop\n")

    seen = {}

    def _spy_apply_change(path):
        seen["resolved"] = config.RESOLVED_CONFIG_DIR
        return {"name": "0001-change", "status": "applied"}

    monkeypatch.setattr(event_bridge, "apply_change", _spy_apply_change)
    result = CliRunner().invoke(
        main, ["db", "apply-change", str(change_file), "--db", "sandbox"]
    )
    assert result.exit_code == 0, result.output
    assert seen["resolved"] == config.sandbox_config_dir("e-1628")


def test_db_backup_pins_main_from_sandbox(tmp_path, monkeypatch):
    """`endless db backup` (no --db) pins main so the backup is taken of the
    real DB, not the sandbox."""
    from endless import event_bridge

    wt = _make_worktree(tmp_path, sandbox=True, task_id="1628")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)

    seen = {}

    def _spy_backup_db():
        seen["resolved"] = config.RESOLVED_CONFIG_DIR
        return {}

    monkeypatch.setattr(event_bridge, "backup_db", _spy_backup_db)
    result = CliRunner().invoke(main, ["db", "backup"])
    assert result.exit_code == 0, result.output
    assert seen["resolved"] == config.main_config_dir()


# --- the gate ------------------------------------------------------------


def test_require_db_context_refuses_in_gated_worktree(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="3")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    with pytest.raises(click.ClickException) as exc:
        config.require_db_context()
    # Click prepends "Error: "; the body is the locked message verbatim.
    assert exc.value.message == config.WORKTREE_DB_REFUSAL
    assert "--db main" in exc.value.message
    assert "--db sandbox" in exc.value.message
    assert "worktree" not in exc.value.message.split("\n")[1]  # value is sandbox, not worktree


def test_require_db_context_ok_when_resolved(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="4")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", tmp_path / "x")
    config.require_db_context()  # must not raise


def test_require_db_context_ok_outside_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.require_db_context()  # not gated -> no flag required


def test_require_db_context_ok_in_non_sandbox_worktree(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=False, task_id="5")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.require_db_context()  # downstream project -> no gate


def test_go_db_context_args(monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    assert config.go_db_context_args() == []
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    assert config.go_db_context_args() == [
        "--config-dir",
        str(config.main_config_dir()),
    ]


# --- position-agnostic global --db (E-1476) ------------------------------


def test_db_flag_position_agnostic(monkeypatch):
    """--db resolves identically before OR after the subcommand."""
    before = CliRunner().invoke(main, ["--db=main", "db", "path"])
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    after = CliRunner().invoke(main, ["db", "path", "--db=main"])
    assert before.exit_code == 0, before.output
    assert after.exit_code == 0, after.output
    assert before.output.strip() == after.output.strip()
    assert before.output.strip().endswith("/.config/endless/endless.db")


def test_db_flag_space_form(monkeypatch):
    """--db <val> (space-separated) is accepted, not just --db=<val>."""
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    result = CliRunner().invoke(main, ["db", "path", "--db", "main"])
    assert result.exit_code == 0, result.output
    assert result.output.strip().endswith("/.config/endless/endless.db")


def test_db_flag_unknown_value_rejected():
    """The retired 'worktree' value is rejected (no backward-compat alias)."""
    result = CliRunner().invoke(main, ["--db=worktree", "db", "path"])
    assert result.exit_code != 0
    assert "unknown --db value" in result.output


# --- `endless db path` (reads the single global --db) --------------------


def test_db_path_main():
    result = CliRunner().invoke(main, ["db", "path", "--db=main"])
    assert result.exit_code == 0, result.output
    assert result.output.strip().endswith("/.config/endless/endless.db")


def test_db_path_sandbox(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1234")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    result = CliRunner().invoke(main, ["db", "path", "--db=sandbox"])
    assert result.exit_code == 0, result.output
    out = result.output.strip()
    assert out.endswith("/sandboxes/e-1234/endless/endless.db")


def test_db_path_sandbox_outside_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    result = CliRunner().invoke(main, ["db", "path", "--db=sandbox"])
    assert result.exit_code != 0
    assert "self-dev worktree" in result.output


def test_db_path_requires_db_flag(monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    result = CliRunner().invoke(main, ["db", "path"])
    assert result.exit_code != 0
    assert "needs an explicit --db" in result.output
