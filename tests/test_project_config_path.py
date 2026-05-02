"""Project-layer config writes must always target the main checkout's
.endless/config.json regardless of which git worktree the command runs in
(E-1111). Tests use real git worktrees because the function shells out to git."""

import json
import subprocess

import pytest

from endless import matchers


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def project_with_worktree(tmp_path):
    main = tmp_path / "main"
    main.mkdir()
    _run(["git", "init", "-q", "-b", "main"], main)
    _run(["git", "config", "user.email", "t@t.t"], main)
    _run(["git", "config", "user.name", "t"], main)

    endless_dir = main / ".endless"
    endless_dir.mkdir()
    cfg_path = endless_dir / "config.json"
    cfg_path.write_text(json.dumps({"name": "p", "matchers": []}))
    _run(["git", "add", "-A"], main)
    _run(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _run(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    return {"main": main, "worktree": worktree, "cfg": cfg_path}


def test_from_main_checkout_returns_main_config(project_with_worktree, monkeypatch):
    monkeypatch.chdir(project_with_worktree["main"])
    result = matchers.project_config_path()
    assert result == project_with_worktree["cfg"]


def test_from_worktree_returns_main_config_not_worktree_copy(project_with_worktree, monkeypatch):
    monkeypatch.chdir(project_with_worktree["worktree"])
    result = matchers.project_config_path()
    assert result == project_with_worktree["cfg"], (
        "Worktree must not resolve to its own .endless/config.json"
    )
    assert "wt" not in str(result), "Path leaked the worktree directory"


def test_writes_from_worktree_land_in_main(project_with_worktree, monkeypatch):
    """The actual bug: writes from a worktree went to the wrong file. Use a
    pivot here (verbs go through a different API as of E-1117); the
    project_config_path() fix applies to all matcher writes."""
    monkeypatch.chdir(project_with_worktree["worktree"])

    matchers.add_match_value(
        type_="pivot", value="testpivotvalue", method="substring",
        machine_only=False,
    )

    main_cfg = json.loads(project_with_worktree["cfg"].read_text())
    matchers_list = main_cfg.get("matchers", [])
    assert any(
        m.get("type") == "pivot" and "testpivotvalue" in (m.get("match") or [])
        for m in matchers_list
    ), "Pivot did not land in main checkout's config"

    wt_cfg_path = project_with_worktree["worktree"] / ".endless" / "config.json"
    if wt_cfg_path.exists():
        wt_data = json.loads(wt_cfg_path.read_text())
        wt_pivots: list[str] = []
        for m in wt_data.get("matchers", []):
            if m.get("type") == "pivot":
                wt_pivots.extend(m.get("match") or [])
        assert "testpivotvalue" not in wt_pivots, (
            "Pivot leaked into worktree's .endless/config.json — bug not fixed"
        )


def test_falls_back_to_walk_up_when_not_in_git_repo(tmp_path, monkeypatch):
    project = tmp_path / "non-git-project"
    endless_dir = project / ".endless"
    endless_dir.mkdir(parents=True)
    cfg = endless_dir / "config.json"
    cfg.write_text("{}")

    nested = project / "src" / "deep"
    nested.mkdir(parents=True)
    monkeypatch.chdir(nested)

    assert matchers.project_config_path() == cfg
