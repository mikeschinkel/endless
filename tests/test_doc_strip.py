"""E-2137: `endless worktree strip-docs` takes mirror commits off task branches.

Endless used to commit a task's plan, outcome, analysis and decision bodies on
the task's BRANCH, where they waited for a land. Measured over 133 worktrees:
123 of 139 genuinely unlanded commits were those mirrors, and 44 of 56 unlanded
worktrees were unlanded only because of them.

Four properties this file pins, in order of how much they would cost to get
wrong:

  1. Real work is never dropped. A commit that bundled a mirror with source
     keeps the source.
  2. A mirror the database does not have is never dropped. Which side is right
     is not this command's to guess.
  3. The rewrite is content-preserving for everything else: a branch's tree
     after the strip differs from before ONLY in the mirror paths.
  4. A worktree standing on the branch is left in step — clean, not showing the
     stripped files as staged additions.
"""

import subprocess
from pathlib import Path

import pytest

from endless import doc_strip


def _run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), capture_output=True, text=True, check=check)


def _git(repo: Path, *args: str) -> str:
    return _run(["git", *args], repo).stdout.strip()


@pytest.fixture
def repo(tmp_path, monkeypatch):
    """A git repo on `main` with one commit, wired so strip_doc_commits runs
    against it: `_project_root` and the default-base lookup both point here.
    """
    root = tmp_path / "proj"
    root.mkdir()
    _run(["git", "init", "-q", "-b", "main", "."], root)
    _run(["git", "config", "user.email", "t@example.com"], root)
    _run(["git", "config", "user.name", "T"], root)
    _run(["git", "config", "commit.gpgsign", "false"], root)
    (root / "README").write_text("hi\n")
    _run(["git", "add", "README"], root)
    _run(["git", "commit", "-q", "-m", "init"], root)

    from endless import worktree_cmd
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: root)
    monkeypatch.setattr(worktree_cmd, "_default_base_branch", lambda _r: "main")
    return root


def _commit_on(repo: Path, branch: str, files: dict[str, str], msg: str):
    """Create or extend `branch` with one commit touching `files`."""
    if _run(["git", "rev-parse", "--verify", "--quiet", f"refs/heads/{branch}"],
            repo, check=False).returncode != 0:
        _run(["git", "branch", branch, "main"], repo)
    _run(["git", "symbolic-ref", "HEAD", f"refs/heads/{branch}"], repo)
    _run(["git", "reset", "-q"], repo)
    _run(["git", "checkout", "-q", "--", "."], repo, check=False)
    for rel, content in files.items():
        target = repo / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
        _run(["git", "add", rel], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)
    _run(["git", "symbolic-ref", "HEAD", "refs/heads/main"], repo)
    _run(["git", "reset", "-q", "--hard", "main"], repo)


def _db_says(monkeypatch, mapping: dict[str, str | None], default=""):
    """Stub the authoritative read. A path absent from `mapping` gets
    `default`; a path mapped to None means "could not ask"."""
    monkeypatch.setattr(
        doc_strip, "doc_mirror_content",
        lambda rel: mapping.get(rel, default))


def _subjects(repo: Path, branch: str) -> list[str]:
    out = _git(repo, "log", "--format=%s", f"main..{branch}")
    return [ln for ln in out.splitlines() if ln.strip()]


def _files_at(repo: Path, rev: str) -> dict[str, str]:
    """path -> exact bytes at rev. Deliberately does NOT strip: a rewrite that
    ate a trailing newline would be a content change, and a helper that trimmed
    would hide it."""
    out = _git(repo, "ls-tree", "-r", "--name-only", rev)
    return {
        rel: _run(["git", "show", f"{rev}:{rel}"], repo).stdout
        for rel in out.splitlines() if rel.strip()
    }


# --- the plain case ---------------------------------------------------------

def test_a_mirror_only_branch_is_emptied(repo, monkeypatch, capsys):
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    assert _subjects(repo, "task/7") == []
    assert _git(repo, "rev-parse", "task/7") == _git(repo, "rev-parse", "main")


def test_every_mirror_kind_and_layout_is_stripped(repo, monkeypatch):
    files = {
        ".endless/plans/E-7.md": "A\n",
        ".endless/outcomes/E-7.md": "B\n",
        ".endless/analyses/E-7.md": "C\n",
        ".endless/decisions/ED-3.md": "D\n",
        ".endless/tasks/e-7/plan.md": "E\n",
    }
    _commit_on(repo, "task/7", files, "Endless: mirrors")
    _db_says(monkeypatch, {rel: body for rel, body in files.items()})

    doc_strip.strip_doc_commits(apply=True)

    assert _subjects(repo, "task/7") == []


def test_dry_run_changes_nothing(repo, monkeypatch, capsys):
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=False)

    assert _git(repo, "rev-parse", "task/7") == before
    out = capsys.readouterr().out
    assert "would strip" in out
    assert "--apply" in out


# --- real work survives -----------------------------------------------------

def test_real_work_commits_are_kept(repo, monkeypatch):
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _commit_on(repo, "task/7", {"src/foo.py": "print('x')\n"}, "E-7: real work")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    assert _subjects(repo, "task/7") == ["E-7: real work"]
    files = _files_at(repo, "task/7")
    assert files["src/foo.py"] == "print('x')\n"
    assert ".endless/plans/E-7.md" not in files


def test_a_commit_bundling_a_mirror_with_source_keeps_the_source(repo, monkeypatch):
    """The reason a commit is dropped by what it CONTAINS rather than by its
    subject line: one commit can be both things."""
    _commit_on(repo, "task/7", {
        ".endless/plans/E-7.md": "PLAN\n",
        "src/foo.py": "print('x')\n",
    }, "E-7: work, and a plan rode along")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    assert _subjects(repo, "task/7") == ["E-7: work, and a plan rode along"]
    files = _files_at(repo, "task/7")
    assert files["src/foo.py"] == "print('x')\n"
    assert ".endless/plans/E-7.md" not in files


def test_nothing_but_the_mirror_paths_changes(repo, monkeypatch):
    """Property 3, stated directly: the branch's tree afterwards differs from
    the tree beforehand only in mirror paths."""
    _commit_on(repo, "task/7", {
        "src/a.py": "a\n", "src/b.py": "b\n", "docs/x.md": "x\n",
    }, "E-7: three files")
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _commit_on(repo, "task/7", {"src/a.py": "a2\n"}, "E-7: edit a")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})
    before = _files_at(repo, "task/7")

    doc_strip.strip_doc_commits(apply=True)

    after = _files_at(repo, "task/7")
    changed = {
        rel for rel in set(before) | set(after)
        if before.get(rel) != after.get(rel)
    }
    assert changed == {".endless/plans/E-7.md"}
    assert after["src/a.py"] == "a2\n"


def test_a_mirror_the_base_already_had_is_restored_not_deleted(repo, monkeypatch):
    """A branch that MODIFIED a mirror main already had must end up agreeing
    with main about it — not proposing its deletion, which would delete main's
    copy at land."""
    (repo / ".endless" / "plans").mkdir(parents=True)
    (repo / ".endless" / "plans" / "E-7.md").write_text("MAIN'S PLAN\n")
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", "plan on main"], repo)

    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "BRANCH PLAN\n"},
               "Endless: update plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "BRANCH PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    assert _files_at(repo, "task/7")[".endless/plans/E-7.md"] == "MAIN'S PLAN\n"
    assert _subjects(repo, "task/7") == []


# --- refusals ---------------------------------------------------------------

def test_a_mirror_the_database_lacks_leaves_the_branch_alone(repo, monkeypatch,
                                                             capsys):
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "ONLY ON THE BRANCH\n"},
               "Endless: add plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "SOMETHING ELSE\n"})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "task/7") == before
    out = capsys.readouterr().out
    assert ".endless/plans/E-7.md" in out
    assert "database does not have" in out


def test_an_unreadable_database_leaves_the_branch_alone(repo, monkeypatch, capsys):
    """"I could not ask" must never read as "the database agrees"."""
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": None})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "task/7") == before
    assert "could not read" in capsys.readouterr().out


def test_a_branch_with_a_merge_is_left_alone(repo, monkeypatch, capsys):
    _commit_on(repo, "side", {"src/side.py": "s\n"}, "side work")
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _run(["git", "symbolic-ref", "HEAD", "refs/heads/task/7"], repo)
    _run(["git", "reset", "-q", "--hard", "task/7"], repo)
    _run(["git", "merge", "-q", "--no-ff", "-m", "merge side", "side"], repo)
    _run(["git", "symbolic-ref", "HEAD", "refs/heads/main"], repo)
    _run(["git", "reset", "-q", "--hard", "main"], repo)
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "task/7") == before
    assert "merge commit" in capsys.readouterr().out


def test_a_branch_with_no_mirror_commits_is_untouched(repo, monkeypatch):
    _commit_on(repo, "task/7", {"src/foo.py": "x\n"}, "E-7: work")
    _db_says(monkeypatch, {})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "task/7") == before


def test_non_task_branches_are_not_considered(repo, monkeypatch):
    _commit_on(repo, "feature/x", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})
    before = _git(repo, "rev-parse", "feature/x")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "feature/x") == before


# --- history the rewrite must preserve --------------------------------------

def test_author_and_dates_survive_the_rewrite(repo, monkeypatch):
    """A rewrite that restamped every retained commit with today's date and the
    sweeping user's name would destroy the history it was meant to tidy."""
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _commit_on(repo, "task/7", {"src/foo.py": "x\n"}, "E-7: real work")
    before = _git(repo, "log", "-1", "--format=%an%x00%ae%x00%aI%x00%B", "task/7")
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    after = _git(repo, "log", "-1", "--format=%an%x00%ae%x00%aI%x00%B", "task/7")
    assert after == before


# --- a worktree standing on the branch --------------------------------------

def test_a_worktree_on_the_branch_is_left_clean(repo, monkeypatch):
    """Moving a ref does not touch the checkout that has it checked out. Without
    the resync, the worktree would report every stripped mirror as a staged
    addition — permanently dirty, which trips land's modified-worktree guard."""
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    _commit_on(repo, "task/7", {"src/foo.py": "x\n"}, "E-7: real work")
    wt = repo / ".endless" / "worktrees" / "e-7"
    _run(["git", "worktree", "add", "-q", str(wt), "task/7"], repo)
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})

    doc_strip.strip_doc_commits(apply=True)

    assert _git(wt, "status", "--porcelain") == ""
    assert not (wt / ".endless" / "plans" / "E-7.md").exists()
    assert (wt / "src" / "foo.py").read_text() == "x\n"


def test_a_worktree_mid_rebase_is_skipped(repo, monkeypatch, capsys):
    """The ref is not ours to move while its own worktree has an operation in
    flight."""
    _commit_on(repo, "task/7", {".endless/plans/E-7.md": "PLAN\n"},
               "Endless: add plan for E-7")
    wt = repo / ".endless" / "worktrees" / "e-7"
    _run(["git", "worktree", "add", "-q", str(wt), "task/7"], repo)
    gitdir = Path(_git(wt, "rev-parse", "--absolute-git-dir"))
    (gitdir / "rebase-merge").mkdir(parents=True)
    _db_says(monkeypatch, {".endless/plans/E-7.md": "PLAN\n"})
    before = _git(repo, "rev-parse", "task/7")

    doc_strip.strip_doc_commits(apply=True)

    assert _git(repo, "rev-parse", "task/7") == before
    assert "rebase is in progress" in capsys.readouterr().out
