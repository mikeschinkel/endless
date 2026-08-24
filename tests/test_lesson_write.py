"""Tests for `endless lesson write` (E-2055).

The behaviour under test is the one the task exists for: a lesson is appended to
the MAIN checkout's `.endless/LESSONS.md` and committed there in the same step,
so recording a correction never waits on `endless worktree land`.
"""

import subprocess

import click
import pytest

from endless import db, lesson_cmd


def _git(args, cwd):
    return subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True, cwd=str(cwd),
    ).stdout.rstrip("\n")


@pytest.fixture
def git_project_at_cwd(isolated_env, monkeypatch):
    """A registered project at cwd, backed by a real git repo with a HEAD."""
    proj_dir = isolated_env["projects_root"]
    (proj_dir / ".endless").mkdir(parents=True, exist_ok=True)
    (proj_dir / ".endless" / "config.json").write_text('{"name": "test"}\n')
    monkeypatch.chdir(proj_dir)
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', ?, 'active', datetime('now'), datetime('now'))",
        (str(proj_dir),),
    )
    _git(["init", "-q", "-b", "main"], cwd=proj_dir)
    _git(["config", "user.email", "test@example.com"], cwd=proj_dir)
    _git(["config", "user.name", "Test"], cwd=proj_dir)
    _git(["config", "commit.gpgsign", "false"], cwd=proj_dir)
    _git(["add", ".endless/config.json"], cwd=proj_dir)
    _git(["commit", "-q", "-m", "init"], cwd=proj_dir)
    return proj_dir


LESSON_TEXT = "- **Rule**: read the code before reinventing the resolver"


# --- the write path --------------------------------------------------------

def test_write_creates_the_log_and_commits_it(git_project_at_cwd):
    log = git_project_at_cwd / ".endless" / "LESSONS.md"
    assert not log.exists(), "fixture must start with no log"

    lesson_cmd.write_lesson("reused the canonical resolver", LESSON_TEXT)

    body = log.read_text()
    assert body.startswith("# Lessons Learned"), "a new log gets the header"
    assert "### [" in body and "reused the canonical resolver" in body
    assert LESSON_TEXT in body
    assert "- **Project**: test" in body, "project is derived, not retyped"

    assert _git(["log", "-1", "--format=%s"], cwd=git_project_at_cwd) == (
        "Endless: record lesson (reused the canonical resolver)"
    )
    assert LESSON_TEXT in _git(["log", "-1", "--format=%b"], cwd=git_project_at_cwd)
    assert _git(["show", "--name-only", "--format=", "HEAD"],
                cwd=git_project_at_cwd) == ".endless/LESSONS.md"
    assert _git(["status", "--porcelain", "--", ".endless/LESSONS.md"],
                cwd=git_project_at_cwd) == "", "the log is clean after the write"


def test_write_appends_below_existing_entries(git_project_at_cwd):
    log = git_project_at_cwd / ".endless" / "LESSONS.md"
    log.write_text("# Lessons Learned\n\n## Entries\n\n### [2026-01-01] Older\n- one\n")
    _git(["add", ".endless/LESSONS.md"], cwd=git_project_at_cwd)
    _git(["commit", "-q", "-m", "seed"], cwd=git_project_at_cwd)

    lesson_cmd.write_lesson("newer thing", LESSON_TEXT)

    body = log.read_text()
    assert body.index("Older") < body.index("newer thing"), "newest last"
    assert body.count("# Lessons Learned") == 1, "existing header is left alone"


def test_write_from_a_worktree_targets_the_main_checkout(git_project_at_cwd, monkeypatch):
    """The whole point: a session inside a worktree writes MAIN's copy.

    Path resolution runs off the registered project row, not a cwd walk-up, so
    the worktree's own checked-out copy is never the target.
    """
    wt = git_project_at_cwd.parent / "wt-e-1"
    _git(["worktree", "add", "-q", str(wt), "-b", "task/1", "main"],
         cwd=git_project_at_cwd)
    monkeypatch.chdir(wt)

    lesson_cmd.write_lesson("written from a worktree", LESSON_TEXT)

    assert "written from a worktree" in (
        git_project_at_cwd / ".endless" / "LESSONS.md"
    ).read_text()
    assert _git(["log", "-1", "--format=%s"], cwd=git_project_at_cwd).startswith(
        "Endless: record lesson"
    )
    # The branch is untouched — no commit, no dirt.
    assert _git(["status", "--porcelain"], cwd=wt) == ""
    assert _git(["rev-parse", "task/1"], cwd=git_project_at_cwd) != _git(
        ["rev-parse", "main"], cwd=git_project_at_cwd
    )


def test_write_preserves_unrelated_dirt_on_main(git_project_at_cwd):
    (git_project_at_cwd / "stray.txt").write_text("user work in flight\n")
    _git(["add", "stray.txt"], cwd=git_project_at_cwd)

    lesson_cmd.write_lesson("kept the dirt", LESSON_TEXT)

    assert _git(["status", "--porcelain", "--", "stray.txt"],
                cwd=git_project_at_cwd).startswith("A ")


# --- refusals --------------------------------------------------------------

def test_summary_over_the_subject_budget_is_refused(git_project_at_cwd):
    too_long = "x" * (lesson_cmd.SUMMARY_LIMIT + 1)
    with pytest.raises(click.ClickException) as exc:
        lesson_cmd.write_lesson(too_long, LESSON_TEXT)
    msg = exc.value.message
    assert str(lesson_cmd.SUBJECT_LIMIT) in msg
    assert str(lesson_cmd.SUMMARY_LIMIT) in msg, "the message names the budget"
    assert not (git_project_at_cwd / ".endless" / "LESSONS.md").exists()


def test_summary_exactly_at_the_budget_is_accepted(git_project_at_cwd):
    at_limit = "y" * lesson_cmd.SUMMARY_LIMIT
    lesson_cmd.write_lesson(at_limit, LESSON_TEXT)
    subject = _git(["log", "-1", "--format=%s"], cwd=git_project_at_cwd)
    assert len(subject) == lesson_cmd.SUBJECT_LIMIT


def test_blank_summary_is_refused(git_project_at_cwd):
    with pytest.raises(click.ClickException):
        lesson_cmd.write_lesson("   ", LESSON_TEXT)


def test_missing_detail_is_refused(git_project_at_cwd):
    with pytest.raises(click.ClickException) as exc:
        lesson_cmd.write_lesson("a summary with no lesson", None)
    assert "--text" in exc.value.message
    assert not (git_project_at_cwd / ".endless" / "LESSONS.md").exists()


def test_unregistered_directory_is_refused(isolated_env, monkeypatch, tmp_path):
    elsewhere = tmp_path / "not-a-project"
    elsewhere.mkdir()
    monkeypatch.chdir(elsewhere)
    with pytest.raises(click.ClickException) as exc:
        lesson_cmd.write_lesson("nowhere to put this", LESSON_TEXT)
    # The resolver speaks for itself — one wording for every command that
    # needs a project, rather than a per-command paraphrase of it.
    assert "Not in a registered project directory" in exc.value.message


def test_commit_failure_keeps_the_append(git_project_at_cwd, monkeypatch):
    """An append-only record must not lose content to a git error: the lesson
    stays on disk and the message says so."""
    real_run = subprocess.run

    def fail_on_commit(args, **kwargs):
        if isinstance(args, (list, tuple)) and "commit" in args:
            return subprocess.CompletedProcess(
                args=args, returncode=1, stdout="", stderr="forced failure\n",
            )
        return real_run(args, **kwargs)

    monkeypatch.setattr(subprocess, "run", fail_on_commit)

    with pytest.raises(click.ClickException) as exc:
        lesson_cmd.write_lesson("commit will fail", LESSON_TEXT)
    assert "forced failure" in exc.value.message
    assert "only the commit failed" in exc.value.message

    log = git_project_at_cwd / ".endless" / "LESSONS.md"
    assert log.exists() and "commit will fail" in log.read_text()


# --- the rendered entry ----------------------------------------------------

def test_entry_shape():
    entry = lesson_cmd._render_entry("summary", "  detail  ", "acme", "2026-08-24")
    assert entry == "\n### [2026-08-24] summary\ndetail\n- **Project**: acme\n"


def test_entry_omits_the_project_bullet_when_unknown():
    entry = lesson_cmd._render_entry("summary", "detail", None, "2026-08-24")
    assert "**Project**" not in entry
