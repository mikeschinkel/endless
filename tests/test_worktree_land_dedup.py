"""Tests for E-1138 / E-1141 / E-1268: auto-commit list inversion +
verbs.jsonl dedup at land.

Two surfaces:
- AUTO_COMMIT_GLOBS registry: verbs.jsonl in, config.json out (E-1141 + E-1268).
- _dedup_worktree_verbs_against_main: bundles worktree verb additions into
  a single commit on the worktree's branch, deduped against main's verbs.

Tests use real git worktrees because the dedup function shells out to git
for status/add/commit.
"""

import fnmatch
import json
import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    AUTO_COMMIT_GLOBS,
    _dedup_worktree_verbs_against_main,
    _is_auto_commit_path,
)


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _verbs(values: list[str]) -> str:
    """Render a verbs.jsonl body for the given list of value strings."""
    lines = [
        json.dumps({"value": v, "definition": f"def {v}"})
        for v in values
    ]
    return "\n".join(lines) + ("\n" if lines else "")


def _read_jsonl(path: Path) -> list[dict]:
    return [
        json.loads(line)
        for line in path.read_text().splitlines()
        if line.strip()
    ]


def _commit_all(repo: Path, msg: str = "x"):
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)


@pytest.fixture
def project_with_worktree(tmp_path):
    """Set up a real git repo with main and one worktree.

    main has .endless/verbs.jsonl committed. The worktree is created from
    main, so it inherits the same verbs.jsonl content as a clean checkout.
    """
    main = tmp_path / "main"
    main.mkdir()
    _run(["git", "init", "-q", "-b", "main"], main)
    _run(["git", "config", "user.email", "t@t.t"], main)
    _run(["git", "config", "user.name", "t"], main)

    endless_dir = main / ".endless"
    endless_dir.mkdir()
    (endless_dir / "config.json").write_text('{"name": "p"}\n')
    (endless_dir / "verbs.jsonl").write_text(_verbs(["add", "fix"]))
    _commit_all(main, "init")

    worktree = tmp_path / "wt"
    _run(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    return {"main": main, "worktree": worktree}


# ---------------------------------------------------------------------------
# AUTO_COMMIT_GLOBS registry (E-1141 + E-1268)
# ---------------------------------------------------------------------------

def test_auto_commit_globs_includes_verbs_jsonl():
    assert any(fnmatch.fnmatch(".endless/verbs.jsonl", p) for p in AUTO_COMMIT_GLOBS)


def test_auto_commit_globs_still_includes_legacy_verbs_json():
    """Legacy verbs.json stays in the glob list during the migration window
    so any stragglers get auto-committed instead of blocking land."""
    assert any(fnmatch.fnmatch(".endless/verbs.json", p) for p in AUTO_COMMIT_GLOBS)


def test_auto_commit_globs_excludes_config():
    assert not any(fnmatch.fnmatch(".endless/config.json", p) for p in AUTO_COMMIT_GLOBS)


def test_is_auto_commit_path_verbs_jsonl():
    assert _is_auto_commit_path(".endless/verbs.jsonl") is True


def test_is_auto_commit_path_config():
    assert _is_auto_commit_path(".endless/config.json") is False


# ---------------------------------------------------------------------------
# _dedup_worktree_verbs_against_main
# ---------------------------------------------------------------------------

def test_dedup_skips_when_verbs_clean(project_with_worktree):
    """No-op: worktree's verbs.jsonl matches what was committed."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]
    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is False


def test_dedup_commits_when_worktree_added_new_verb(project_with_worktree):
    """Worktree adds a new verb; dedup commits it on the worktree's branch."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]
    (worktree / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix", "rewrite"]))

    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is True

    log = subprocess.run(
        ["git", "log", "--oneline", "-1"],
        cwd=worktree, capture_output=True, text=True, check=True,
    ).stdout
    assert "bundle worktree verb additions" in log

    on_disk = _read_jsonl(worktree / ".endless" / "verbs.jsonl")
    values = [e["value"] for e in on_disk]
    assert values == ["add", "fix", "rewrite"]


def test_dedup_skips_when_worktree_only_has_main_verbs(project_with_worktree):
    """Worktree's verbs.jsonl was rewritten with only main's existing values
    (no new entries) — after dedup the on-disk content matches the committed
    HEAD, so no commit is created."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]
    (worktree / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix"]))

    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is False

    on_disk = _read_jsonl(worktree / ".endless" / "verbs.jsonl")
    values = [e["value"] for e in on_disk]
    assert values == ["add", "fix"]


def test_dedup_handles_main_having_new_verbs_too(project_with_worktree):
    """Main has gained a new verb (post-worktree-creation) and worktree has
    its own additions. Dedup result is the union, in order: main first
    (preserving its order), then worktree's new entries."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]

    (main / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix", "deploy"]))
    _commit_all(main, "main adds deploy")

    (worktree / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix", "rewrite"]))

    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is True

    on_disk = _read_jsonl(worktree / ".endless" / "verbs.jsonl")
    values = [e["value"] for e in on_disk]
    assert values == ["add", "fix", "deploy", "rewrite"]


def test_dedup_handles_overlap_with_main_new_verb(project_with_worktree):
    """Both main and worktree added the same new verb. After dedup the
    worktree's verbs.jsonl has main's view (no duplicate)."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]

    (main / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix", "rewrite"]))
    _commit_all(main, "main adds rewrite")
    (worktree / ".endless" / "verbs.jsonl").write_text(_verbs(["add", "fix", "rewrite"]))

    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is True

    on_disk = _read_jsonl(worktree / ".endless" / "verbs.jsonl")
    values = [e["value"] for e in on_disk]
    assert values == ["add", "fix", "rewrite"]


def test_dedup_returns_false_when_no_verbs_file(project_with_worktree):
    """If the worktree has no .endless/verbs.jsonl at all, dedup is a no-op."""
    main = project_with_worktree["main"]
    worktree = project_with_worktree["worktree"]
    (worktree / ".endless" / "verbs.jsonl").unlink()
    result = _dedup_worktree_verbs_against_main(worktree, main)
    assert result is False
