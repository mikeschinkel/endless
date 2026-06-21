"""Tests for the `endless task spawn --bg` soft-throttle warning (E-1572).

Before dispatching a background agent, `_bg_throttle_warn` counts the bg agents
already `working` for the project and, if the count meets the configured
threshold (`bg_throttle_warn` in `.endless/config.json`, default 3, <= 0
disables), emits a 3-line advisory to stderr. It NEVER blocks dispatch.

The count is read from the `session-query count-bg-agents` Go helper; these
tests mock both the config read and that subprocess so no DB or binary is
touched. The warning text and the "does it warn at all" decision live in Python,
so that is what is exercised here. The count query's correctness is covered
Go-side in internal/monitor/count_bg_agents_test.go.
"""

import subprocess
from pathlib import Path

from endless import task_cmd
from endless import config
from endless import event_bridge


class _FakeProc:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _patch(monkeypatch, *, threshold, active):
    """Wire up `_bg_throttle_warn`'s dependencies: project config returns the
    given threshold (None means the key is absent → default), and the
    count-bg-agents helper returns `active`."""
    cfg = {} if threshold is None else {"bg_throttle_warn": threshold}
    monkeypatch.setattr(config, "resolution_cwd", lambda: Path("/proj"))
    monkeypatch.setattr(config, "project_config_read", lambda p: cfg)
    monkeypatch.setattr(config, "go_db_context_args", lambda: [])
    monkeypatch.setattr(event_bridge, "_resolve_endless_go", lambda: "endless-go")

    def fake_run(cmd, **kw):
        assert "count-bg-agents" in cmd
        return _FakeProc(0, f"{active}\n", "")

    monkeypatch.setattr(subprocess, "run", fake_run)


def test_below_threshold_no_warning(monkeypatch, capsys):
    _patch(monkeypatch, threshold=3, active=2)
    task_cmd._bg_throttle_warn(1572)
    assert capsys.readouterr().err == ""


def test_at_threshold_warns(monkeypatch, capsys):
    _patch(monkeypatch, threshold=3, active=3)
    task_cmd._bg_throttle_warn(1572)
    err = capsys.readouterr().err
    assert "3 bg agents already active" in err
    assert "threshold: 3" in err
    # All three advisory lines present.
    assert "parallel-execution slot" in err
    assert "sweet spot" in err


def test_over_threshold_warns_once(monkeypatch, capsys):
    _patch(monkeypatch, threshold=3, active=5)
    task_cmd._bg_throttle_warn(1572)
    err = capsys.readouterr().err
    assert "5 bg agents already active" in err
    # Exactly one warning block, not one-per-overage: a single "warning:" line.
    assert err.count("warning:") == 1


def test_threshold_zero_disables(monkeypatch, capsys):
    _patch(monkeypatch, threshold=0, active=99)
    task_cmd._bg_throttle_warn(1572)
    assert capsys.readouterr().err == ""


def test_negative_threshold_disables(monkeypatch, capsys):
    _patch(monkeypatch, threshold=-1, active=99)
    task_cmd._bg_throttle_warn(1572)
    assert capsys.readouterr().err == ""


def test_default_threshold_when_unset(monkeypatch, capsys):
    # No bg_throttle_warn key → default of 3; 3 active should warn.
    _patch(monkeypatch, threshold=None, active=3)
    task_cmd._bg_throttle_warn(1572)
    assert "threshold: 3" in capsys.readouterr().err


def test_count_helper_failure_is_silent(monkeypatch, capsys):
    # A non-zero exit from the count helper must not warn and must not raise.
    monkeypatch.setattr(config, "resolution_cwd", lambda: Path("/proj"))
    monkeypatch.setattr(config, "project_config_read", lambda p: {"bg_throttle_warn": 1})
    monkeypatch.setattr(config, "go_db_context_args", lambda: [])
    monkeypatch.setattr(event_bridge, "_resolve_endless_go", lambda: "endless-go")
    monkeypatch.setattr(subprocess, "run",
                        lambda cmd, **kw: _FakeProc(1, "", "boom"))
    task_cmd._bg_throttle_warn(1572)
    assert capsys.readouterr().err == ""
