"""Tests for the E-1429 explicit --db gate inside self-dev worktrees."""

import click
import pytest
from click.testing import CliRunner

from endless import config
from endless.cli import main


def _make_worktree(tmp_path, sandbox: bool, task_id: str = "555"):
    """Build <tmp>/proj/.endless/{config.json, worktrees/e-<id>} and return the
    worktree dir. config.json sets worktree_sandbox to `sandbox`."""
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    wt = endless / "worktrees" / f"e-{task_id}"
    wt.mkdir(parents=True)
    (endless / "config.json").write_text(
        '{"worktree_sandbox": %s}\n' % ("true" if sandbox else "false")
    )
    return wt


# --- resolution helpers --------------------------------------------------


def test_worktree_task_id_detects_segment():
    assert config.worktree_task_id(
        __import__("pathlib").Path("/x/proj/.endless/worktrees/e-1429/internal")
    ) == "1429"
    assert config.worktree_task_id(
        __import__("pathlib").Path("/x/proj/.endless/worktrees/e-77-some-slug")
    ) == "77"
    assert config.worktree_task_id(__import__("pathlib").Path("/x/proj")) is None


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


def test_apply_db_choice_worktree(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="909")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    config.apply_db_choice("worktree")
    assert config.RESOLVED_CONFIG_DIR == config.sandbox_config_dir("909")
    assert config.RESOLVED_CONFIG_DIR.name == "endless"
    assert "sandboxes/worktree-e-909/endless" in str(config.RESOLVED_CONFIG_DIR)


def test_apply_db_choice_worktree_outside_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)  # not a worktree
    with pytest.raises(ValueError):
        config.apply_db_choice("worktree")


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
    assert "--db worktree" in exc.value.message


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


# --- `endless db path` ---------------------------------------------------


def test_db_path_main():
    result = CliRunner().invoke(main, ["db", "path", "--db=main"])
    assert result.exit_code == 0, result.output
    assert result.output.strip().endswith("/.config/endless/endless.db")


def test_db_path_worktree(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1234")
    monkeypatch.chdir(wt)
    result = CliRunner().invoke(main, ["db", "path", "--db=worktree"])
    assert result.exit_code == 0, result.output
    out = result.output.strip()
    assert out.endswith("/sandboxes/worktree-e-1234/endless/endless.db")


def test_db_path_worktree_outside_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    result = CliRunner().invoke(main, ["db", "path", "--db=worktree"])
    assert result.exit_code != 0
    assert "self-dev worktree" in result.output


def test_db_path_requires_db_flag():
    result = CliRunner().invoke(main, ["db", "path"])
    assert result.exit_code != 0  # --db is required
