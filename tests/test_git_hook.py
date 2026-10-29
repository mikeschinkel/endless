"""The Endless-Task trailer on task-branch commits, and its hook setup (E-2490).

The trailer tests commit through real Git hooks that call this checkout's
`endless`, so they prove what a user's commit gets, not what a function
returns. Setup is tested against lefthook's files without lefthook itself,
except the last test, which runs only where lefthook is installed.
"""

import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

from endless import git_hook


def _git(cwd, *args, check=True, env=None):
    return subprocess.run(["git", *args], cwd=str(cwd), capture_output=True,
                          text=True, check=check, env=env)


@pytest.fixture
def repo(tmp_path):
    root = tmp_path / "repo"
    root.mkdir()
    _git(root, "init", "-q", "-b", "main")
    _git(root, "config", "user.email", "test@example.com")
    _git(root, "config", "user.name", "Test")
    _git(root, "config", "commit.gpgsign", "false")
    _git(root, "commit", "-q", "--allow-empty", "-m", "init")
    return root


@pytest.fixture
def endless_on_path(monkeypatch):
    """Put this checkout's `endless` first on PATH, as the hooks call it."""
    bindir = Path(sys.executable).parent
    assert (bindir / "endless").exists()
    monkeypatch.setenv("PATH", f"{bindir}{os.pathsep}{os.environ['PATH']}")


@pytest.fixture
def hooked(repo, endless_on_path):
    """A repo set up with Endless's own Git hooks, on branch task/42."""
    out = git_hook.setup(repo, "none", interactive=False, lefthook_file="local")
    assert out.set_up
    _git(repo, "checkout", "-q", "-b", "task/42")
    return repo


def _message(repo):
    return _git(repo, "log", "-1", "--format=%B").stdout


def _trailers(repo):
    return _git(repo, "log", "-1",
                "--format=%(trailers:key=Endless-Task,valueonly)").stdout.split()


# --- The trailer -----------------------------------------------------------

def test_message_commit_gets_trailer_and_keeps_subject(hooked):
    _git(hooked, "commit", "-q", "--allow-empty", "-m", "E-42: do the thing")
    assert _message(hooked).startswith("E-42: do the thing\n")
    assert _trailers(hooked) == ["E-42"]


def test_editor_commit_gets_trailer(hooked, tmp_path):
    editor = tmp_path / "editor.sh"
    editor.write_text('#!/bin/sh\n{ echo "E-42: typed"; cat "$1"; } > "$1.t"; '
                      'mv "$1.t" "$1"\n')
    editor.chmod(0o755)
    _git(hooked, "commit", "-q", "--allow-empty",
         env={**os.environ, "GIT_EDITOR": str(editor)})
    assert _message(hooked).startswith("E-42: typed\n")
    assert _trailers(hooked) == ["E-42"]


def test_empty_editor_message_still_aborts(hooked):
    before = _git(hooked, "rev-parse", "HEAD").stdout
    res = _git(hooked, "commit", "--allow-empty", check=False,
               env={**os.environ, "GIT_EDITOR": "true"})
    assert res.returncode != 0
    assert "empty commit message" in res.stderr
    assert _git(hooked, "rev-parse", "HEAD").stdout == before


def test_existing_trailer_is_not_doubled(hooked):
    _git(hooked, "commit", "-q", "--allow-empty", "-m",
         "E-42: x\n\nEndless-Task: E-42")
    _git(hooked, "commit", "-q", "--allow-empty", "--amend", "--no-edit")
    assert _trailers(hooked) == ["E-42"]


def test_no_trailer_off_task_branches(hooked):
    _git(hooked, "checkout", "-q", "main")
    _git(hooked, "commit", "-q", "--allow-empty", "-m", "on main")
    assert _trailers(hooked) == []
    _git(hooked, "checkout", "-q", "-b", "feature/42")
    _git(hooked, "commit", "-q", "--allow-empty", "-m", "on a feature")
    assert _trailers(hooked) == []
    _git(hooked, "checkout", "-q", "--detach")
    _git(hooked, "commit", "-q", "--allow-empty", "-m", "detached")
    assert _trailers(hooked) == []


def test_task_worktree_commit_gets_trailer(hooked, tmp_path):
    """Hook files live in the common git dir, so a linked worktree runs them."""
    wt = tmp_path / "wt"
    _git(hooked, "worktree", "add", "-q", "-b", "task/7", str(wt))
    _git(wt, "commit", "-q", "--allow-empty", "-m", "E-7: in a worktree")
    assert _trailers(wt) == ["E-7"]


def test_hook_command_exits_zero_on_failure(hooked):
    res = subprocess.run(
        ["endless", "git-hook", "commit-msg", str(hooked / "no-such-file")],
        cwd=str(hooked), capture_output=True, text=True)
    assert res.returncode == 0
    assert "trailer not added" in res.stderr


def test_commit_succeeds_without_endless_installed(hooked):
    """The hook file's guard: no `endless` on PATH is no trailer, not a failed
    commit."""
    env = {**os.environ, "PATH": "/usr/bin:/bin"}
    git = shutil.which("git", path=env["PATH"])
    if git is None:
        pytest.skip("no git under /usr/bin or /bin")
    _git(hooked, "commit", "-q", "--allow-empty", "-m", "no endless", env=env)
    assert _trailers(hooked) == []


def test_manager_line_succeeds_without_endless():
    res = subprocess.run(
        ["/bin/sh", "-c", git_hook.manager_line("commit-msg"), "hook", "msg"],
        capture_output=True, env={"PATH": "/nonexistent"})
    assert res.returncode == 0


# --- What a repo has -------------------------------------------------------

def test_inspect_classifies_managers(repo):
    assert git_hook.inspect(repo).manager == "none"

    hook = repo / ".git" / "hooks" / "pre-commit"
    hook.write_text("#!/bin/sh\nexit 0\n")
    r = git_hook.inspect(repo)
    assert (r.manager, r.existing) == ("hooks", ["pre-commit"])
    hook.unlink()

    (repo / ".pre-commit-config.yaml").write_text("repos: []\n")
    assert (git_hook.inspect(repo).manager, git_hook.inspect(repo).other) == (
        "other", "pre-commit")
    (repo / ".pre-commit-config.yaml").unlink()

    _git(repo, "config", "core.hooksPath", "custom-hooks")
    assert git_hook.inspect(repo).manager == "other"
    _git(repo, "config", "core.hooksPath", ".husky/_")
    assert git_hook.inspect(repo).manager == "husky"
    _git(repo, "config", "--unset", "core.hooksPath")

    (repo / "lefthook.yml").write_text("pre-commit:\n")
    assert git_hook.inspect(repo).manager == "lefthook"


def test_inspect_outside_git_is_none(tmp_path):
    plain = tmp_path / "plain"
    plain.mkdir()
    assert git_hook.inspect(plain) is None


# --- Setup -----------------------------------------------------------------

def test_plain_setup_is_idempotent_and_marked(repo):
    for _ in range(2):
        assert git_hook.setup(repo, "none", False, "local").set_up
    for event in git_hook.EVENTS:
        hook = repo / ".git" / "hooks" / event
        assert git_hook.is_endless_hook(hook)
        assert os.access(hook, os.X_OK)
    assert git_hook.problem(git_hook.inspect(repo)) is None


def test_infer_without_choice_writes_plain_hooks(repo):
    assert git_hook.setup(repo, None, interactive=False,
                          lefthook_file="local").set_up


def test_declined_lefthook_leaves_existing_hooks_alone(repo):
    hook = repo / ".git" / "hooks" / "pre-commit"
    hook.write_text("#!/bin/sh\necho mine\n")
    out = git_hook.setup(repo, None, interactive=False, lefthook_file="local")
    assert not out.set_up
    assert "will not get the Endless-Task trailer" in out.lines[-1][1]
    assert hook.read_text() == "#!/bin/sh\necho mine\n"
    assert not (repo / ".git" / "hooks" / "prepare-commit-msg").exists()
    assert not (repo / "lefthook.yml").exists()


def _no_lefthook(monkeypatch):
    real = shutil.which
    monkeypatch.setattr(git_hook.shutil, "which",
                        lambda name, *a, **k: None if name == "lefthook"
                        else real(name, *a, **k))


def test_lefthook_local_setup_without_lefthook(repo, monkeypatch):
    _no_lefthook(monkeypatch)
    out = git_hook.setup(repo, "lefthook", False, "local")
    assert not out.set_up
    text = "\n".join(t for _, t in out.lines)
    assert "lefthook is not installed" in text
    assert "brew install lefthook" in text
    local = (repo / "lefthook-local.yml").read_text()
    for event in git_hook.EVENTS:
        assert f'run: "endless git-hook {event} {{0}}"' in local
    # lefthook reads the local file only beside a main config.
    assert (repo / "lefthook.yml").is_file()
    assert "endless git-hook" not in (repo / "lefthook.yml").read_text()
    # Excluded, so no checkout reports it untracked.
    assert "lefthook-local.yml" not in _git(repo, "status", "--porcelain").stdout


def test_lefthook_committed_setup_guards_endless(repo, monkeypatch):
    _no_lefthook(monkeypatch)
    (repo / "lefthook.yml").write_text("pre-commit:\n  commands:\n"
                                       "    lint:\n      run: 'true'\n")
    git_hook.setup(repo, None, False, "committed")
    text = (repo / "lefthook.yml").read_text()
    assert text.startswith("pre-commit:\n  commands:\n")
    assert ("command -v endless >/dev/null || exit 0; "
            "endless git-hook prepare-commit-msg {0}") in text
    assert not (repo / "lefthook-local.yml").exists()
    # Re-running adds nothing.
    git_hook.setup(repo, None, False, "committed")
    assert (repo / "lefthook.yml").read_text() == text


def test_lefthook_file_listing_the_event_is_left_for_the_user(repo, monkeypatch):
    _no_lefthook(monkeypatch)
    original = "prepare-commit-msg:\n  commands:\n    mine:\n      run: 'true'\n"
    (repo / "lefthook.yml").write_text(original)
    out = git_hook.setup(repo, None, False, "committed")
    assert not out.set_up
    assert any("Add these commands to it yourself" in t for _, t in out.lines)
    text = (repo / "lefthook.yml").read_text()
    assert text.startswith(original)
    assert text.count("prepare-commit-msg:") == 1  # no duplicate key


def test_lefthook_never_claims_a_foreign_hook_event(repo, monkeypatch):
    """lefthook renames a foreign hook file for any event its config names."""
    _no_lefthook(monkeypatch)
    (repo / "lefthook.yml").write_text("pre-commit:\n")
    foreign = repo / ".git" / "hooks" / "commit-msg"
    foreign.write_text("#!/bin/sh\necho gerrit\n")
    out = git_hook.setup(repo, None, False, "local")
    assert not out.set_up
    local = (repo / "lefthook-local.yml").read_text()
    assert "commit-msg:" not in local.replace("prepare-commit-msg:", "")
    assert "endless git-hook prepare-commit-msg" in local
    assert any(git_hook.manager_line("commit-msg") in t for _, t in out.lines)
    assert foreign.read_text() == "#!/bin/sh\necho gerrit\n"


def test_migration_runs_renamed_hooks_from_local(repo, monkeypatch):
    _no_lefthook(monkeypatch)
    (repo / ".git" / "hooks" / "pre-commit").write_text("#!/bin/sh\n")
    git_hook.setup(repo, "lefthook", False, "committed")
    hooks = Path(_git(repo, "rev-parse", "--path-format=absolute",
                      "--git-path", "hooks").stdout.strip())
    local = (repo / "lefthook-local.yml").read_text()
    assert f"'{hooks / 'pre-commit.old'}' {{0}}" in local
    # The clone-specific path never reaches the committed file.
    assert str(hooks) not in (repo / "lefthook.yml").read_text()
    assert "endless git-hook commit-msg" in (repo / "lefthook.yml").read_text()


def test_husky_gets_lines_and_no_edits(repo):
    (repo / ".husky").mkdir()
    out = git_hook.setup(repo, "lefthook", False, "local")
    assert not out.set_up
    text = out.lines[0][1]
    assert ".husky/prepare-commit-msg" in text
    assert git_hook.manager_line("prepare-commit-msg") in text
    assert list((repo / ".husky").iterdir()) == []
    assert "does not run" in git_hook.problem(git_hook.inspect(repo))

    for event in git_hook.EVENTS:
        (repo / ".husky" / event).write_text(git_hook.manager_line(event) + "\n")
    assert git_hook.problem(git_hook.inspect(repo)) is None


def test_other_manager_cannot_be_confirmed(repo):
    _git(repo, "config", "core.hooksPath", "elsewhere")
    out = git_hook.setup(repo, None, False, "local")
    assert not out.set_up
    assert "core.hooksPath (elsewhere)" in out.lines[0][1]
    assert "cannot confirm" in git_hook.problem(git_hook.inspect(repo))
    assert not (repo / "elsewhere").exists()


def test_copy_lefthook_local_copies_only_untracked(repo, tmp_path):
    (repo / "lefthook-local.yml").write_text("x: 1\n")
    wt = tmp_path / "wt"
    _git(repo, "worktree", "add", "-q", "-b", "task/9", str(wt))
    assert git_hook.copy_lefthook_local(repo, wt) == ["lefthook-local.yml"]
    assert (wt / "lefthook-local.yml").read_text() == "x: 1\n"
    # Never overwrites what the worktree already has.
    (wt / "lefthook-local.yml").write_text("mine\n")
    assert git_hook.copy_lefthook_local(repo, wt) == []
    assert (wt / "lefthook-local.yml").read_text() == "mine\n"


def test_status_command_exit_codes(repo, endless_on_path):
    res = subprocess.run(["endless", "git-hook", "status", str(repo)],
                         capture_output=True, text=True)
    assert res.returncode == 1
    assert "will not get the Endless-Task trailer" in res.stderr
    git_hook.setup(repo, "none", False, "local")
    res = subprocess.run(["endless", "git-hook", "status", str(repo)],
                         capture_output=True, text=True)
    assert res.returncode == 0, res.stdout + res.stderr
    assert "get the Endless-Task trailer" in res.stdout


@pytest.mark.skipif(shutil.which("lefthook") is None,
                    reason="lefthook is not installed")
def test_real_lefthook_adds_trailer_in_a_task_worktree(repo, endless_on_path,
                                                       tmp_path):
    out = git_hook.setup(repo, "lefthook", False, "local")
    assert out.set_up, out.lines
    _git(repo, "add", "lefthook.yml")
    _git(repo, "commit", "-q", "-m", "lefthook")
    wt = tmp_path / "wt"
    _git(repo, "worktree", "add", "-q", "-b", "task/5", str(wt))
    git_hook.copy_lefthook_local(repo, wt)
    _git(wt, "commit", "-q", "--allow-empty", "-m", "E-5: via lefthook")
    assert _trailers(wt) == ["E-5"]


# --- project init ----------------------------------------------------------

def test_project_init_sets_up_plain_hooks(isolated_env, git_repo):
    from endless.register import register_project
    proj = git_repo(isolated_env["projects_root"] / "plain")
    register_project(proj, infer=True)
    for event in git_hook.EVENTS:
        assert git_hook.is_endless_hook(proj / ".git" / "hooks" / event)


def test_project_init_commits_the_lefthook_yml_it_creates(isolated_env,
                                                          git_repo,
                                                          monkeypatch):
    from endless.register import register_project
    _no_lefthook(monkeypatch)
    proj = git_repo(isolated_env["projects_root"] / "lh")
    register_project(proj, infer=True, git_hooks="lefthook")
    committed = _git(proj, "show", "--name-only", "--format=", "HEAD").stdout
    assert "lefthook.yml" in committed.split()
    assert _git(proj, "status", "--porcelain").stdout == ""
