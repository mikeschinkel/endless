"""Project-layer config path resolves to the nearest .endless/config.json
walking up from cwd (E-1140, reverses E-1112). Inside a worktree this means
writes go to the worktree's own .endless/config.json — which exists because
git checked it out from main when the worktree was created. Tests use real
git worktrees because the prior implementation shelled out to git."""

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
    return {"main": main, "worktree": worktree, "main_cfg": cfg_path}


def test_from_main_checkout_returns_main_config(project_with_worktree, monkeypatch):
    monkeypatch.chdir(project_with_worktree["main"])
    result = matchers.project_config_path()
    assert result == project_with_worktree["main_cfg"]


def test_from_worktree_returns_worktree_config_not_main(project_with_worktree, monkeypatch):
    """E-1140: from inside a worktree, resolve to the worktree's own
    .endless/config.json (which git checked out from main), not main's."""
    monkeypatch.chdir(project_with_worktree["worktree"])
    result = matchers.project_config_path()
    expected = project_with_worktree["worktree"] / ".endless" / "config.json"
    assert result == expected, (
        "Worktree must resolve to its own .endless/config.json, not main's"
    )
    assert result != project_with_worktree["main_cfg"]


def test_writes_from_worktree_land_in_worktree_not_main(project_with_worktree, monkeypatch):
    """E-1140 (reverses E-1112): writes from a worktree dirty the worktree's
    own config, not main's. Use a pivot here (verbs go through a different
    API as of E-1117); project_config_path() resolution applies to all
    matcher writes."""
    monkeypatch.chdir(project_with_worktree["worktree"])

    matchers.add_match_value(
        type_="pivot", value="testpivotvalue", method="substring",
        machine_only=False,
    )

    wt_cfg_path = project_with_worktree["worktree"] / ".endless" / "config.json"
    wt_data = json.loads(wt_cfg_path.read_text())
    wt_pivots: list[str] = []
    for m in wt_data.get("matchers", []):
        if m.get("type") == "pivot":
            wt_pivots.extend(m.get("match") or [])
    assert "testpivotvalue" in wt_pivots, (
        "Pivot did not land in worktree's .endless/config.json"
    )

    main_cfg = json.loads(project_with_worktree["main_cfg"].read_text())
    main_pivots: list[str] = []
    for m in main_cfg.get("matchers", []):
        if m.get("type") == "pivot":
            main_pivots.extend(m.get("match") or [])
    assert "testpivotvalue" not in main_pivots, (
        "Pivot leaked into main's .endless/config.json — anchor-to-main was not fully reverted"
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
