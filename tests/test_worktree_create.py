"""Tests for E-971 Layer F: per-task worktree auto-creation helpers.

Covers _slugify_title, _default_base_branch, and _check_plan_file_committed.
The end-to-end create_task_worktree flow is exercised via test_task_claim_worktree.
"""

import json
import subprocess
from pathlib import Path

import pytest

from endless.worktree_cmd import (
    _check_plan_file_committed,
    _default_base_branch,
    _run_post_worktree_create_hook,
    _slugify_title,
)


# ---------------------------------------------------------------------------
# _slugify_title
# ---------------------------------------------------------------------------

def test_slugify_normal_title():
    out = _slugify_title("Move title verbs from hardcoded list to database table")
    assert out == "move-title-verbs-hardcoded-list-database"


def test_slugify_all_filler_falls_back_to_task():
    assert _slugify_title("The to from") == "task"


def test_slugify_whitespace_only_falls_back_to_task():
    assert _slugify_title("   ") == "task"


def test_slugify_empty_string_falls_back_to_task():
    assert _slugify_title("") == "task"


def test_slugify_drops_punctuation():
    out = _slugify_title("Edit user's profile (UI/UX)")
    assert out == "edit-user-s-profile-ui-ux"


def test_slugify_collapses_repeated_separators():
    out = _slugify_title("foo___bar---baz")
    assert out == "foo-bar-baz"


def test_slugify_truncates_at_word_boundary():
    title = "alpha bravo charlie delta echo foxtrot golf hotel india juliet"
    out = _slugify_title(title)
    assert len(out) <= 40
    assert not out.endswith("-")
    # Confirm we cut at a hyphen, not mid-word
    assert "-" in out
    assert all(part for part in out.split("-"))


def test_slugify_short_title_passes_through():
    assert _slugify_title("Add tests") == "add-tests"


def test_slugify_lowercases():
    assert _slugify_title("FOO BAR") == "foo-bar"


# ---------------------------------------------------------------------------
# _default_base_branch
# ---------------------------------------------------------------------------

def test_default_base_branch_returns_origin_head(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        assert args == ["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"]
        return "refs/remotes/origin/develop"

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "develop"


def test_default_base_branch_falls_back_to_main(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        raise subprocess.CalledProcessError(1, ["git"], stderr="not set")

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "main"


def test_default_base_branch_handles_empty_ref(monkeypatch, tmp_path):
    from endless import worktree_cmd

    def fake_git(args, cwd):
        return ""

    monkeypatch.setattr(worktree_cmd, "_git", fake_git)
    assert _default_base_branch(tmp_path) == "main"


# ---------------------------------------------------------------------------
# _check_plan_file_committed
# ---------------------------------------------------------------------------

def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def git_repo(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)
    return repo


def test_check_plan_file_absent_returns_none(git_repo):
    assert _check_plan_file_committed(1170, git_repo) is None


def test_check_plan_file_committed_clean_returns_none(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    _run(["git", "add", ".endless/plans/E-1170.md"], git_repo)
    _run(["git", "commit", "-q", "-m", "add plan"], git_repo)
    assert _check_plan_file_committed(1170, git_repo) is None


def test_check_plan_file_untracked_returns_message(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    msg = _check_plan_file_committed(1170, git_repo)
    assert msg is not None
    assert ".endless/plans/E-1170.md" in msg
    assert "git -C" in msg
    assert "endless task claim E-1170" in msg


def test_check_plan_file_modified_tracked_returns_message(git_repo):
    plans = git_repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / "E-1170.md").write_text("plan content\n")
    _run(["git", "add", ".endless/plans/E-1170.md"], git_repo)
    _run(["git", "commit", "-q", "-m", "add plan"], git_repo)
    (plans / "E-1170.md").write_text("plan content modified\n")
    msg = _check_plan_file_committed(1170, git_repo)
    assert msg is not None
    assert ".endless/plans/E-1170.md" in msg


# --- E-1301: task ID extraction from worktree path -------------------------

from pathlib import Path

from endless.worktree_cmd import _task_id_from_worktree_path, _warn_if_companion_disagrees


def test_task_id_from_worktree_path_basic_match():
    p = Path("/Users/x/Projects/foo/.endless/worktrees/e-967")
    assert _task_id_from_worktree_path(p) == "E-967"


def test_task_id_from_worktree_path_ignores_named_alternate():
    # ED-1515: only the canonical bare `e-NNN` dir is recognized; a
    # named-alternate `e-NNN-slug` no longer resolves as the task's worktree.
    p = Path("/Users/x/Projects/foo/.endless/worktrees/e-1208-record-verbs")
    assert _task_id_from_worktree_path(p) is None


def test_task_id_from_worktree_path_subdir():
    p = Path("/Users/x/Projects/foo/.endless/worktrees/e-967/src/main.go")
    assert _task_id_from_worktree_path(p) == "E-967"


def test_task_id_from_worktree_path_main_returns_none():
    p = Path("/Users/x/Projects/foo")
    assert _task_id_from_worktree_path(p) is None


def test_task_id_from_worktree_path_no_match():
    p = Path("/Users/x/Projects/foo/.endless/worktrees/random-name")
    assert _task_id_from_worktree_path(p) is None


def test_task_id_from_worktree_path_no_digits():
    p = Path("/Users/x/Projects/foo/.endless/worktrees/e-abc")
    assert _task_id_from_worktree_path(p) is None


def test_warn_if_companion_disagrees_silent_when_matches(capsys):
    p = Path("/x/.endless/worktrees/e-100")
    _warn_if_companion_disagrees(p, {"task_id": "E-100"})
    captured = capsys.readouterr()
    assert captured.err == ""


def test_warn_if_companion_disagrees_warns_on_disagreement(capsys):
    # Simulates the E-1186 stale-companion case: path says E-100 but
    # the companion's task_id field claims E-1186.
    p = Path("/x/.endless/worktrees/e-100")
    _warn_if_companion_disagrees(p, {"task_id": "E-1186"})
    captured = capsys.readouterr()
    assert "stale companion" in captured.err
    assert "E-1186" in captured.err
    assert "E-100" in captured.err


def test_warn_if_companion_disagrees_silent_when_field_absent(capsys):
    # New companions (post-E-1301) omit task_id. No warning.
    p = Path("/x/.endless/worktrees/e-100")
    _warn_if_companion_disagrees(p, {"kind": "task", "base_branch": "main"})
    captured = capsys.readouterr()
    assert captured.err == ""


def test_warn_if_companion_disagrees_silent_when_companion_none(capsys):
    p = Path("/x/.endless/worktrees/e-100")
    _warn_if_companion_disagrees(p, None)
    captured = capsys.readouterr()
    assert captured.err == ""


# ---------------------------------------------------------------------------
# _run_post_worktree_create_hook (E-986)
# ---------------------------------------------------------------------------

def _write_hook(project_root: Path, body: str, *, executable: bool = True) -> Path:
    hooks = project_root / ".endless" / "hooks"
    hooks.mkdir(parents=True, exist_ok=True)
    hook = hooks / "post-worktree-create.sh"
    hook.write_text(body)
    if executable:
        hook.chmod(0o755)
    return hook


def test_hook_runs_with_worktree_arg_and_cwd(tmp_path, capsys):
    project = tmp_path / "proj"
    project.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_hook(
        project,
        "#!/usr/bin/env bash\n"
        f"printf 'ARG=%s\\n' \"$1\" > '{sentinel}'\n"
        f"printf 'CWD=%s\\n' \"$(pwd)\" >> '{sentinel}'\n",
    )
    _run_post_worktree_create_hook(project, worktree)
    # cwd may resolve symlinks (macOS /tmp), so compare against the realpath.
    real_wt = worktree.resolve()
    assert sentinel.read_text() == f"ARG={worktree}\nCWD={real_wt}\n"


def test_hook_failure_is_non_fatal_and_loud(tmp_path, capsys):
    project = tmp_path / "proj"
    project.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    _write_hook(project, "#!/usr/bin/env bash\nexit 7\n")
    # Must not raise — worktree is kept.
    _run_post_worktree_create_hook(project, worktree)
    err = capsys.readouterr().err
    assert "post-worktree-create" in err
    assert "exited 7" in err
    assert str(worktree) in err


def test_no_hook_is_a_silent_noop(tmp_path, capsys):
    project = tmp_path / "proj"
    project.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    _run_post_worktree_create_hook(project, worktree)
    out = capsys.readouterr()
    assert out.out == ""
    assert out.err == ""


def test_non_executable_hook_warns_and_does_not_run(tmp_path, capsys):
    project = tmp_path / "proj"
    project.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_hook(
        project,
        f"#!/usr/bin/env bash\ntouch '{sentinel}'\n",
        executable=False,
    )
    _run_post_worktree_create_hook(project, worktree)
    err = capsys.readouterr().err
    assert "not executable" in err
    assert not sentinel.exists()
