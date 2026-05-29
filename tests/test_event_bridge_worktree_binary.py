"""E-1510: event_bridge must exec <worktree>/bin/endless-go when --db sandbox
is the active context for a self-dev worktree.

Main's compiled binary embeds main's schema.sql. Using it against the
worktree's sandbox DB silently misses any additive schema the branch
declares, surfacing later as 'no such table' errors. event_bridge now
prefers the worktree-built binary in that exact case and fails loudly when
the binary is missing — silent fallback would re-introduce the bug.

These tests verify all three schema-mutating shell-outs (apply_change,
backup_db, emit_event) route through the same _resolve_endless_go helper.
"""

import json
from pathlib import Path

import click
import pytest

from endless import config, event_bridge


def _make_worktree_layout(tmp_path: Path, task_id: str = "9999",
                          binary_present: bool = True) -> tuple[Path, Path]:
    """Build a synthetic self-dev worktree at <tmp>/proj/.endless/worktrees/e-<id>.

    Returns (worktree_dir, worktree_bin_endless_go_path). When binary_present
    is True, the bin/endless-go file is created and made executable; when
    False, only the bin/ directory is created so the resolver can name the
    expected path but the existence check fails.
    """
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    (endless).mkdir(parents=True)
    (endless / "config.json").write_text('{"worktree_sandbox": true}\n')
    wt = endless / "worktrees" / f"e-{task_id}"
    (wt / "bin").mkdir(parents=True)
    wt_bin = wt / "bin" / "endless-go"
    if binary_present:
        wt_bin.write_text("#!/bin/sh\necho fake\n")
        wt_bin.chmod(0o755)
    return wt, wt_bin


@pytest.fixture
def synthetic_sandbox(tmp_path, monkeypatch):
    """Stand up a synthetic self-dev worktree, point XDG_CACHE_HOME at tmp so
    sandbox_config_dir resolves under it, set RESOLVED_CONFIG_DIR to that
    sandbox path, chdir into the worktree, and stub subprocess.run.

    Yields a dict with: worktree, worktree_bin, sandbox_dir, calls (list of
    cmd argv lists captured from subprocess.run).
    """
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / ".cache"))
    wt, wt_bin = _make_worktree_layout(tmp_path, task_id="9999")
    monkeypatch.chdir(wt)
    sandbox_dir = config.sandbox_config_dir("9999")
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", sandbox_dir)

    calls: list[list[str]] = []

    class _FakeResult:
        returncode = 0
        stdout = "{}"
        stderr = ""

    def _fake_run(cmd, capture_output=False, text=False):
        calls.append(list(cmd))
        return _FakeResult()

    monkeypatch.setattr(event_bridge.subprocess, "run", _fake_run)
    return {
        "worktree": wt,
        "worktree_bin": wt_bin,
        "sandbox_dir": sandbox_dir,
        "calls": calls,
    }


# --- apply_change -------------------------------------------------------------


def test_apply_change_uses_worktree_binary_under_db_sandbox(synthetic_sandbox):
    event_bridge.apply_change("ignored")
    assert len(synthetic_sandbox["calls"]) == 1
    cmd = synthetic_sandbox["calls"][0]
    assert cmd[0] == str(synthetic_sandbox["worktree_bin"])


def test_backup_db_uses_worktree_binary_under_db_sandbox(synthetic_sandbox):
    event_bridge.backup_db()
    assert len(synthetic_sandbox["calls"]) == 1
    cmd = synthetic_sandbox["calls"][0]
    assert cmd[0] == str(synthetic_sandbox["worktree_bin"])


def test_emit_event_uses_worktree_binary_under_db_sandbox(
    synthetic_sandbox, monkeypatch
):
    # Seed node_id and a project row so emit_event's preflight succeeds.
    config_path = synthetic_sandbox["sandbox_dir"] / "config.json"
    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(json.dumps({"node_id": "test"}))
    # Stub the projects-table read.
    monkeypatch.setattr(
        event_bridge, "_get_or_create_node_id", lambda: "test"
    )
    monkeypatch.setattr(
        "endless.db.query", lambda *a, **kw: [{"path": "/tmp/proj"}]
    )
    event_bridge.emit_event(
        kind="task.added",
        project="sample",
        entity_type="task",
        entity_id=1,
        payload={"title": "x"},
        actor_kind="system",
        session_id="42",
    )
    assert len(synthetic_sandbox["calls"]) == 1
    cmd = synthetic_sandbox["calls"][0]
    assert cmd[0] == str(synthetic_sandbox["worktree_bin"])


# --- missing-binary loud failure ----------------------------------------------


def test_missing_worktree_binary_fails_loudly(tmp_path, monkeypatch):
    """--db sandbox active + worktree bin/endless-go absent → ClickException
    with a 'just build' hint. Silent fallback to main's binary would
    re-introduce the schema-baseline mismatch."""
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / ".cache"))
    wt, wt_bin = _make_worktree_layout(
        tmp_path, task_id="9999", binary_present=False
    )
    monkeypatch.chdir(wt)
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", config.sandbox_config_dir("9999")
    )
    with pytest.raises(click.ClickException) as exc:
        event_bridge.apply_change("ignored")
    msg = exc.value.message
    # PRODUCT: surface the underlying `go build` command, never the `just`
    # wrapper — shipped code paths must not reference the dev-side Justfile.
    assert "go build -o bin/endless-go ./cmd/endless-go" in msg
    assert "just" not in msg
    assert str(wt_bin) in msg


# --- PATH fallback branches ---------------------------------------------------


def test_db_main_uses_path_lookup(tmp_path, monkeypatch):
    """--db main → RESOLVED_CONFIG_DIR == main_config_dir() → PATH fallback."""
    wt, wt_bin = _make_worktree_layout(tmp_path)
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    monkeypatch.setattr(
        event_bridge.shutil, "which", lambda _name: "/fake/global-endless-go"
    )
    calls: list[list[str]] = []

    class _FakeResult:
        returncode = 0
        stdout = "{}"
        stderr = ""

    monkeypatch.setattr(
        event_bridge.subprocess, "run",
        lambda cmd, **kw: (calls.append(list(cmd)), _FakeResult())[1],
    )
    event_bridge.apply_change("ignored")
    assert calls[0][0] == "/fake/global-endless-go"


def test_no_db_context_uses_path_lookup(tmp_path, monkeypatch):
    """RESOLVED_CONFIG_DIR is None → PATH fallback (matches the just-land
    invocation path from main where no --db is passed)."""
    monkeypatch.chdir(tmp_path)  # not a worktree
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    monkeypatch.setattr(
        event_bridge.shutil, "which", lambda _name: "/fake/global-endless-go"
    )
    calls: list[list[str]] = []

    class _FakeResult:
        returncode = 0
        stdout = "{}"
        stderr = ""

    monkeypatch.setattr(
        event_bridge.subprocess, "run",
        lambda cmd, **kw: (calls.append(list(cmd)), _FakeResult())[1],
    )
    event_bridge.apply_change("ignored")
    assert calls[0][0] == "/fake/global-endless-go"


def test_cwd_outside_worktree_uses_path_lookup(tmp_path, monkeypatch):
    """RESOLVED_CONFIG_DIR set but cwd NOT in a self-dev worktree → PATH
    fallback. (Defensive — apply_db_choice('sandbox') refuses outside a
    worktree, but other RESOLVED_CONFIG_DIR sources may not.)"""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", tmp_path / "some" / "external" / "dir"
    )
    monkeypatch.setattr(
        event_bridge.shutil, "which", lambda _name: "/fake/global-endless-go"
    )
    calls: list[list[str]] = []

    class _FakeResult:
        returncode = 0
        stdout = "{}"
        stderr = ""

    monkeypatch.setattr(
        event_bridge.subprocess, "run",
        lambda cmd, **kw: (calls.append(list(cmd)), _FakeResult())[1],
    )
    event_bridge.apply_change("ignored")
    assert calls[0][0] == "/fake/global-endless-go"


# --- config.resolved_worktree_endless_go directly -----------------------------


def test_resolver_helper_returns_none_when_unresolved(monkeypatch):
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", None)
    assert config.resolved_worktree_endless_go() is None


def test_resolver_helper_returns_none_for_main_dir(tmp_path, monkeypatch):
    wt, _ = _make_worktree_layout(tmp_path)
    monkeypatch.chdir(wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.main_config_dir())
    assert config.resolved_worktree_endless_go() is None


def test_resolver_helper_returns_path_for_sandbox(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / ".cache"))
    wt, wt_bin = _make_worktree_layout(tmp_path, task_id="7777")
    monkeypatch.chdir(wt)
    monkeypatch.setattr(
        config, "RESOLVED_CONFIG_DIR", config.sandbox_config_dir("7777")
    )
    result = config.resolved_worktree_endless_go()
    assert result == wt_bin
