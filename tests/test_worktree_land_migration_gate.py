"""Tests for E-2184: land refuses when main gained migrations since the fork.

The gate itself is Go (internal/landgate, `endless-go worktree land-gate`) and
is covered there. These cover the Python side of the seam: that land asks it,
renders its refusal through the one renderer, brackets it for an agent, and
fails CLOSED when the gate cannot answer.

The end-to-end cases drive the real endless-go this checkout built, over real
git repos, because the property is "a collision git rebases cleanly is still
refused" — a fact only git can supply.
"""

import os
import subprocess
from pathlib import Path

import click
import pytest

from endless import agent_env, agent_help
from endless.worktree_cmd import (
    LAND_GATE_BLOCK_END,
    LAND_GATE_BLOCK_START,
    _refuse_if_land_gated,
    render_land_gate_refusal,
)

REPO_ROOT = Path(__file__).resolve().parent.parent
ENDLESS_GO = REPO_ROOT / "bin" / "endless-go"


@pytest.fixture(autouse=True)
def _pin_agent_view(monkeypatch):
    monkeypatch.setattr(agent_help, "_AGENT_VIEW", False)


@pytest.fixture
def as_agent(monkeypatch):
    monkeypatch.setenv(agent_env.ENTRYPOINT_VAR, agent_env.CLI_ENTRYPOINT)


# ─── the renderer ───────────────────────────────────────────────────────────


def test_human_gets_the_summary_then_the_marked_block():
    out = render_land_gate_refusal("cannot land E-1: collision", "step one\nstep two\n")
    lines = out.split("\n")
    assert lines[0] == "cannot land E-1: collision"
    assert lines[2] == LAND_GATE_BLOCK_START
    assert lines[3:5] == ["step one", "step two"]
    assert lines[5] == LAND_GATE_BLOCK_END
    assert out.count("cannot land E-1: collision") == 1


def test_agent_gets_the_verdict_at_both_ends(as_agent):
    out = render_land_gate_refusal("cannot land E-1: collision", "fix it\n")
    lines = out.split("\n")
    assert lines[0] == "[Endless] worktree land: cannot land E-1: collision"
    assert lines[-1] == "Error: " + lines[0]
    assert LAND_GATE_BLOCK_START in out and "fix it" in out


def test_no_block_means_no_markers():
    out = render_land_gate_refusal("cannot land E-1: nope", "")
    assert LAND_GATE_BLOCK_START not in out


# ─── the seam, end to end ───────────────────────────────────────────────────


def _run(cmd, cwd):
    return subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True, text=True)


def _commit(repo: Path, msg: str, files: dict[str, str]):
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)


@pytest.fixture
def repos(tmp_path, monkeypatch, on_path):
    if not os.access(ENDLESS_GO, os.X_OK):
        pytest.skip("bin/endless-go is not built; run `just build`")
    on_path(ENDLESS_GO)
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "home" / ".config"))
    main = tmp_path / "main"
    main.mkdir()
    _run(["git", "init", "-q", "-b", "main"], main)
    _run(["git", "config", "user.email", "t@t.t"], main)
    _run(["git", "config", "user.name", "t"], main)
    _commit(main, "init", {
        ".endless/config.json": '{"name":"p","migrations":{"dirs":["db/migrations"]}}\n',
        "db/migrations/00001_base.sql": "-- base\n",
    })
    wt = tmp_path / "wt"
    _run(["git", "worktree", "add", "-q", str(wt), "-b", "task/1", "main"], main)
    return main, wt


@pytest.fixture
def on_path(monkeypatch):
    """Put a given endless-go first on PATH, as the installed one."""
    def put(binary: Path):
        d = binary.parent / "path-bin"
        d.mkdir(exist_ok=True)
        link = d / "endless-go"
        if link.exists() or link.is_symlink():
            link.unlink()
        link.symlink_to(binary)
        monkeypatch.setenv("PATH", f"{d}{os.pathsep}{os.environ['PATH']}")
    return put


def _gate(main, wt):
    _refuse_if_land_gated(main, wt, "main", "E-1")


def test_collision_is_refused_with_the_rename(repos):
    main, wt = repos
    _commit(wt, "branch", {"db/migrations/00002_branch.sql": "x\n"})
    _commit(main, "main", {"db/migrations/00002_main.sql": "x\n"})
    with pytest.raises(click.ClickException) as ei:
        _gate(main, wt)
    msg = ei.value.message
    assert msg.startswith("cannot land E-1:")
    assert "db/migrations/00002_branch.sql  →  db/migrations/00003_branch.sql" in msg
    assert "endless worktree land E-1" in msg


def test_no_collision_passes(repos):
    main, wt = repos
    _commit(wt, "branch", {"db/migrations/00002_branch.sql": "x\n"})
    _commit(main, "main", {"README.md": "x\n"})
    _gate(main, wt)


def test_gate_that_cannot_answer_refuses(repos, tmp_path, on_path):
    main, wt = repos
    broken = tmp_path / "broken" / "endless-go"
    broken.parent.mkdir()
    broken.write_text("#!/bin/sh\necho boom >&2\nexit 1\n")
    broken.chmod(0o755)
    on_path(broken)
    with pytest.raises(click.ClickException) as ei:
        _gate(main, wt)
    assert "could not decide" in ei.value.message
    assert "boom" in ei.value.message
