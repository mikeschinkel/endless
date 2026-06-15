"""Tests for `endless internal template render` (E-1565).

The hidden `internal` group exposes the Go renderer via a debug-friendly
Python CLI surface. The renderer is exercised via CliRunner with JSON on
stdin.
"""

import json

import pytest
from click.testing import CliRunner

from endless.cli import main as cli_main


@pytest.fixture(autouse=True)
def chdir_to_template_project(tmp_path, monkeypatch):
    """Renderer resolves project root from cwd; give it a `.endless/` dir."""
    (tmp_path / ".endless").mkdir()
    monkeypatch.chdir(tmp_path)


def test_internal_template_render_handoff_outputs_substituted_text():
    runner = CliRunner()
    vars_payload = {
        "spawned_id": 2026,
        "title": "CLI smoke",
        "spawner_task": 1565,
        "return_anchor": "%3",
        "worktree_path": "/tmp/wt",
        "branch": "task/2026-cli-smoke",
    }
    result = runner.invoke(
        cli_main,
        ["internal", "template", "render", "handoff"],
        input=json.dumps(vars_payload),
    )
    assert result.exit_code == 0, result.output
    assert "E-2026" in result.output
    assert "CLI smoke" in result.output
    assert result.output.strip() != ""


def test_internal_template_render_unknown_template_errors():
    runner = CliRunner()
    result = runner.invoke(
        cli_main,
        ["internal", "template", "render", "no-such-template"],
        input="{}",
    )
    assert result.exit_code != 0
