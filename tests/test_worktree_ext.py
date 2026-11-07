"""Tests for the worktree extension point (E-2563).

The core runs a list of extensions after a worktree is created and before a
session starts in an existing one. It knows nothing about any language; the
Go behaviour lives in `go_support` and has tests of its own.
"""

import inspect
import json
import os
import subprocess
from pathlib import Path

import pytest

from endless import worktree_ext


@pytest.fixture
def registry(monkeypatch):
    """A clean, built-ins-already-loaded registry the test fills itself."""
    exts: list[worktree_ext.Extension] = []
    monkeypatch.setattr(worktree_ext, "_extensions", exts)
    monkeypatch.setattr(worktree_ext, "_builtins_loaded", True)
    return exts


# ── running ────────────────────────────────────────────────────────────────

def test_every_extension_runs_in_order_with_the_worktree(registry, tmp_path):
    seen = []
    worktree_ext.register("first", lambda wt: seen.append(("first", wt)))
    worktree_ext.register("second", lambda wt: seen.append(("second", wt)))
    worktree_ext.run_extensions(tmp_path)
    assert seen == [("first", tmp_path), ("second", tmp_path)]


def test_a_failing_extension_is_named_and_does_not_stop_the_rest(
    registry, tmp_path, capsys,
):
    seen = []

    def boom(wt):
        raise RuntimeError("go is not on PATH")

    worktree_ext.register("go-support", boom)
    worktree_ext.register("after", lambda wt: seen.append(wt))
    worktree_ext.run_extensions(tmp_path)  # must not raise
    assert seen == [tmp_path]
    err = capsys.readouterr().err
    assert "go-support" in err
    assert str(tmp_path).replace(str(Path.home()), "~") in err
    assert "go is not on PATH" in err
    assert "usable without it" in err


def test_registering_a_name_twice_replaces_it(registry, tmp_path):
    seen = []
    worktree_ext.register("x", lambda wt: seen.append(1))
    worktree_ext.register("x", lambda wt: seen.append(2))
    worktree_ext.run_extensions(tmp_path)
    assert seen == [2]


def test_the_builtin_go_extension_is_registered():
    assert [e.name for e in worktree_ext.registered()] == ["go-support"]


def test_the_core_names_no_language():
    """P2: the core holds the list of built-in modules and nothing about what
    any of them does — no go.mod, go.work, `go` tool, or any other stack."""
    source = inspect.getsource(worktree_ext)
    for word in ("go.mod", "go.work", "GOWORK", "npm", "cargo", "venv"):
        assert word not in source, word


# ── where the core calls it ────────────────────────────────────────────────

def test_bootstrap_runs_extensions_before_the_create_hook(
    registry, tmp_path, monkeypatch,
):
    """Order matters: Endless's own create hook regenerates go.work with more
    in it, so the extension's copy must be written first."""
    from endless import sandbox_cmd, worktree_cmd

    order = []
    worktree_ext.register("ext", lambda wt: order.append(("ext", wt)))
    monkeypatch.setattr(worktree_cmd, "_run_post_worktree_create_hook",
                        lambda root, wt: order.append(("create-hook", wt)))
    monkeypatch.setattr(sandbox_cmd, "reset_after_create",
                        lambda wt: order.append(("reset", wt)))
    monkeypatch.setattr(worktree_cmd, "provision_worktree_sandbox", lambda wt: None)
    monkeypatch.setattr(worktree_cmd, "copy_lefthook_local", lambda root, wt: None)
    wt = tmp_path / "wt"
    wt.mkdir()
    worktree_cmd._bootstrap_task_worktree(1, wt, "main", "task/1", tmp_path)
    assert order == [("ext", wt), ("create-hook", wt), ("reset", wt)]


def _managed_worktree(project_root: Path, task_id: int) -> Path:
    """An existing Endless-managed worktree directory: the path convention
    plus the companion marker, which is all `create_task_worktree` checks."""
    wt = project_root / ".endless" / "worktrees" / f"e-{task_id}"
    (wt / ".endless").mkdir(parents=True)
    (wt / ".endless" / "worktree.json").write_text(json.dumps({
        "kind": "task", "base_branch": "main", "branch": f"task/{task_id}",
    }))
    return wt


def test_an_existing_worktree_runs_extensions_on_claim_and_spawn(
    registry, tmp_path, monkeypatch,
):
    """`task claim`, `task spawn` and `task prime` all go through
    `create_task_worktree`, whose already-exists branch is the one a worktree
    created before this change takes."""
    from endless import worktree_cmd
    from endless.worktree_cmd import create_task_worktree

    # Resolved before the exists-check, from a repository this test has none of.
    monkeypatch.setattr(worktree_cmd, "_default_base_branch", lambda root: "main")
    seen = []
    worktree_ext.register("ext", lambda wt: seen.append(wt))
    wt = _managed_worktree(tmp_path, 7)
    got, created = create_task_worktree(7, tmp_path)
    assert (got, created) == (wt, False)
    assert seen == [wt]


def test_an_existing_worktree_runs_extensions_on_recreate(registry, tmp_path):
    from endless.worktree_cmd import recreate_dropped_worktree

    seen = []
    worktree_ext.register("ext", lambda wt: seen.append(wt))
    wt = _managed_worktree(tmp_path, 8)
    assert recreate_dropped_worktree(8, tmp_path, "main", detached=False) == wt
    assert seen == [wt]


def test_resume_runs_extensions_in_the_existing_worktree(
    registry, tmp_path, monkeypatch,
):
    """`session resume` and `session goto --resume` share `_resolve_resume`,
    and a live worktree takes its first branch."""
    from endless import session_cmd

    wt = tmp_path / "wt"
    wt.mkdir()
    seen = []
    worktree_ext.register("ext", lambda p: seen.append(p))
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: {
            "session_id": "uuid-1", "worktree_path": str(wt),
            "endless_id": 5, "task_id": 10, "project_id": 1,
        },
    )
    decision: dict = {}
    session_cmd._resolve_resume("E-10", decision_out=decision)
    assert seen == [wt]
    assert decision["worktree"] == str(wt)


def test_a_failing_extension_does_not_stop_the_resume(
    registry, tmp_path, monkeypatch, capsys,
):
    from endless import session_cmd

    wt = tmp_path / "wt"
    wt.mkdir()

    def boom(p):
        raise RuntimeError("nope")

    worktree_ext.register("ext", boom)
    monkeypatch.setattr(
        session_cmd, "_resume_target",
        lambda ref: {
            "session_id": "uuid-1", "worktree_path": str(wt),
            "endless_id": 5, "task_id": 10, "project_id": 1,
        },
    )
    uuid, worktree, label, eid = session_cmd._resolve_resume("E-10")
    assert (uuid, worktree, label, eid) == ("uuid-1", str(wt), "E-10", 5)
    assert "worktree extension ext failed" in capsys.readouterr().err
