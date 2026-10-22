"""E-2349: re-running `endless project init` brings a project registered at any
earlier time up to Endless usage, and running it again changes nothing."""

import re
import subprocess
from pathlib import Path

import click
import pytest

from endless import db
from endless.project_path import stored
from endless.register import (
    BRING_UP_COMMIT_SUBJECT, GITIGNORE_BLOCK_HEADER, GITIGNORE_ENTRIES,
    GUIDE_POINTER_MARKER, register_project, scaffold_gitignore,
    scaffold_guide_pointer,
)

from conftest import init_git_repo

REPO_ROOT = Path(__file__).resolve().parent.parent


def _git(path: Path, *args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=str(path), check=True, capture_output=True,
        text=True,
    ).stdout


def _repo(path: Path) -> Path:
    """A repo with one commit and a committer, so bring-up can commit."""
    init_git_repo(path)
    _git(path, "config", "user.email", "test@example.com")
    _git(path, "config", "user.name", "Test")
    return path


def _commits(path: Path) -> int:
    return int(_git(path, "rev-list", "--count", "HEAD").strip())


def _status(path: Path) -> str:
    return _git(path, "status", "--porcelain")


# --- .gitignore ---------------------------------------------------------------

def test_gitignore_entries_include_logs():
    assert ".endless/logs/" in GITIGNORE_ENTRIES


def test_rerun_on_an_older_gitignore_adds_only_the_missing_lines(tmp_path):
    older = [e for e in GITIGNORE_ENTRIES if e != ".endless/logs/"]
    (tmp_path / ".gitignore").write_text(
        "*.out\n\n" + GITIGNORE_BLOCK_HEADER + "\n" + "\n".join(older) + "\n"
    )
    assert scaffold_gitignore(tmp_path) == [".endless/logs/"]
    text = (tmp_path / ".gitignore").read_text()
    for entry in GITIGNORE_ENTRIES:
        assert text.count(entry + "\n") == 1, entry
    assert scaffold_gitignore(tmp_path) == []


# Every top-level name under `.endless/` that the source names, by what it is.
# A path Endless writes but never commits must be gitignored by project init
# (GITIGNORE_ENTRIES), or carry its own ignore file; the rest are committed or
# are Endless's own repo layout. A new name fails the test below until it is
# classified here, which is the point: a runtime path cannot slip in unignored.
COMMITTED = {
    "config.json", "db-ledger", "tasks", "decisions", "verbs.jsonl",
    "LESSONS.md", "templates", "hooks", "migrations", "extensions",
    "verify", "verify.toml", "data.sql",
    # Retired document-mirror directories, still named by migrations and
    # cleanup code.
    "plans", "analyses", "outcomes", "reasons", "notes",
}
SELF_IGNORING = {"sandbox"}  # writes its own .gitignore of `*`


def _source_endless_names() -> set[str]:
    patterns = [
        re.compile(r"""["']\.endless/([A-Za-z0-9_.-]+)"""),
        re.compile(r"""["']\.endless["'],\s*["']([A-Za-z0-9_.-]+)["']"""),
        re.compile(r"""["']\.endless["']\s*/\s*["']([A-Za-z0-9_.-]+)["']"""),
    ]
    names: set[str] = set()
    files = [*(REPO_ROOT / "src" / "endless").rglob("*.py"),
             *(REPO_ROOT / "internal").rglob("*.go"),
             *(REPO_ROOT / "cmd").rglob("*.go")]
    for f in files:
        if f.name.endswith("_test.go"):
            continue
        text = f.read_text()
        for p in patterns:
            names.update(m.group(1) for m in p.finditer(text))
    return names


def test_every_endless_path_the_source_names_is_classified():
    ignored = {e.removeprefix(".endless/").rstrip("/")
               for e in GITIGNORE_ENTRIES}
    known = ignored | COMMITTED | SELF_IGNORING
    unclassified = sorted(_source_endless_names() - known)
    assert not unclassified, (
        f"{unclassified} are named under .endless/ but not classified: add a "
        "runtime path to register.GITIGNORE_ENTRIES, or a committed one to "
        "COMMITTED in this test"
    )


# --- worktree refusal ---------------------------------------------------------

def test_init_refuses_a_linked_worktree_and_writes_nothing(isolated_env):
    main = _repo(isolated_env["projects_root"] / "owner")
    register_project(main, infer=True)
    wt = isolated_env["projects_root"] / "elsewhere"
    _git(main, "worktree", "add", "-q", str(wt))

    status_before = _status(wt)

    with pytest.raises(click.ClickException) as exc:
        register_project(wt, infer=True)
    assert "worktree of project 'owner'" in exc.value.format_message()
    assert _status(wt) == status_before
    assert not db.query("SELECT 1 FROM projects WHERE path = ?", (stored(wt),))


def test_init_refuses_a_directory_under_endless_worktrees(isolated_env):
    main = _repo(isolated_env["projects_root"] / "holder")
    register_project(main, infer=True)
    stray = main / ".endless" / "worktrees" / "e-1"
    stray.mkdir(parents=True)

    with pytest.raises(click.ClickException) as exc:
        register_project(stray, infer=True)
    assert "worktree of project 'holder'" in exc.value.format_message()
    assert not (stray / ".endless").exists()


# --- guide pointer -------------------------------------------------------------

def test_guide_pointer_creates_claude_md_when_neither_file_exists(tmp_path):
    assert scaffold_guide_pointer(tmp_path) == "CLAUDE.md"
    text = (tmp_path / "CLAUDE.md").read_text()
    assert GUIDE_POINTER_MARKER in text and "endless guide" in text
    assert not (tmp_path / "AGENTS.md").exists()


def test_guide_pointer_appends_to_an_existing_file_without_it(tmp_path):
    (tmp_path / "AGENTS.md").write_text("# Rules\n\nBe nice.")
    assert scaffold_guide_pointer(tmp_path) == "AGENTS.md"
    text = (tmp_path / "AGENTS.md").read_text()
    assert text.startswith("# Rules\n\nBe nice.\n\n" + GUIDE_POINTER_MARKER)
    assert not (tmp_path / "CLAUDE.md").exists()


def test_guide_pointer_leaves_a_file_that_mentions_it_alone(tmp_path):
    (tmp_path / "CLAUDE.md").write_text("Run `endless guide` first.\n")
    (tmp_path / "AGENTS.md").write_text("# Other\n")
    assert scaffold_guide_pointer(tmp_path) is None
    assert (tmp_path / "CLAUDE.md").read_text() == "Run `endless guide` first.\n"
    assert (tmp_path / "AGENTS.md").read_text() == "# Other\n"


# --- commit rule and idempotence ------------------------------------------------

def test_bring_up_commits_what_it_wrote_and_a_rerun_changes_nothing(isolated_env):
    proj = _repo(isolated_env["projects_root"] / "legacy")
    before = _commits(proj)

    register_project(proj, infer=True)
    assert _commits(proj) == before + 1
    assert _git(proj, "log", "-1", "--format=%s").strip() == BRING_UP_COMMIT_SUBJECT
    committed = set(_git(proj, "show", "--name-only", "--format=", "HEAD").split())
    assert committed == {
        ".endless/config.json", ".endless/tasks/CLAUDE.md", ".gitignore",
        "CLAUDE.md", ".claude/output-styles/Endless.md",
    }
    assert _status(proj) == ""

    snapshot = {p: p.read_bytes() for p in proj.rglob("*")
                if p.is_file() and ".git" not in p.parts}
    register_project(proj, infer=True)
    assert _commits(proj) == before + 1
    assert _status(proj) == ""
    assert {p: p.read_bytes() for p in proj.rglob("*")
            if p.is_file() and ".git" not in p.parts} == snapshot


def test_a_never_committed_config_is_committed(isolated_env):
    """The legacy case: config.json written long ago and never committed."""
    proj = _repo(isolated_env["projects_root"] / "old")
    (proj / ".endless").mkdir()
    (proj / ".endless" / "config.json").write_text('{"name": "old"}\n')

    register_project(proj, infer=True)
    assert ".endless/config.json" in _git(
        proj, "show", "--name-only", "--format=", "HEAD").split()


def test_user_files_with_their_own_changes_are_left_out(isolated_env, capsys):
    proj = _repo(isolated_env["projects_root"] / "dirty")
    (proj / ".gitignore").write_text("*.out\n")
    (proj / "CLAUDE.md").write_text("# Mine\n")
    _git(proj, "add", ".gitignore", "CLAUDE.md")
    _git(proj, "commit", "-q", "-m", "user files")
    (proj / ".gitignore").write_text("*.out\n*.tmp\n")  # the user's own edit

    register_project(proj, infer=True)
    committed = set(_git(proj, "show", "--name-only", "--format=", "HEAD").split())
    assert ".gitignore" not in committed
    assert "CLAUDE.md" in committed  # clean before the run, so committed
    assert ".endless/config.json" in committed
    assert ".gitignore" in _status(proj)
    out = capsys.readouterr().out
    assert "Not committed: .gitignore (it had uncommitted changes of yours)" in out


def test_nothing_is_committed_off_the_default_branch(isolated_env, capsys):
    proj = _repo(isolated_env["projects_root"] / "branched")
    _git(proj, "checkout", "-q", "-b", "feature")
    before = _commits(proj)

    register_project(proj, infer=True)
    assert _commits(proj) == before
    out = capsys.readouterr().out
    assert "not the default branch main" in out
