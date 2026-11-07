"""Tests for the Go worktree extension (E-2563).

A task worktree sits inside its main checkout, so a `go.work` there is found
from the worktree and Go refuses to build. The extension writes the
worktree its own `go.work`, listing every module in it and nothing else.

These run the real `go` tool and real git, because the defect being fixed is
how `go` resolves a workspace from a directory, which a stub can only assume.
"""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

from endless import go_support, worktree_ext

pytestmark = pytest.mark.skipif(
    shutil.which("go") is None, reason="the go tool is not on PATH",
)

GITIGNORE_COVERING = "/bin/\ngo.work\ngo.work.sum\n"
GITIGNORE_LACKING = "/bin/\n"


def _run(cmd, cwd, env=None, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True,
        env=env,
    )


def _go_env():
    env = {k: v for k, v in os.environ.items() if k != "GOWORK"}
    env["GOTOOLCHAIN"] = "local"
    return env


def _go_build(cwd):
    """`go build ./...` in `cwd`, as a developer would run it."""
    return _run(["go", "build", "./..."], cwd, env=_go_env(), check=False)


def _module(dir: Path, path: str, *, nested_pkg: bool = False):
    dir.mkdir(parents=True, exist_ok=True)
    (dir / "go.mod").write_text(f"module {path}\n\ngo 1.21\n")
    if nested_pkg:
        (dir / "lib.go").write_text("package lib\n")
    else:
        (dir / "main.go").write_text("package main\n\nfunc main() {}\n")


@pytest.fixture
def checkout(tmp_path, monkeypatch):
    """A main checkout with a `go.work` of its own, and a task worktree
    inside it holding a root module and a nested `test/` module."""
    monkeypatch.delenv("GOWORK", raising=False)

    def make(gitignore: str = GITIGNORE_COVERING, *, go_work: bool = True):
        main = tmp_path / "main"
        _module(main, "example.com/m")
        _module(main / "test", "example.com/m/test", nested_pkg=True)
        (main / ".gitignore").write_text(gitignore)
        _run(["git", "init", "-q", "-b", "main"], main)
        _run(["git", "config", "user.email", "t@t.t"], main)
        _run(["git", "config", "user.name", "t"], main)
        _run(["git", "add", "-A"], main)
        _run(["git", "commit", "-q", "-m", "init"], main)
        if go_work:
            # The main checkout's own workspace: what breaks the worktree.
            (main / "go.work").write_text("go 1.21\n\nuse .\n")
        wt = main / ".endless" / "worktrees" / "e-1"
        wt.parent.mkdir(parents=True)
        _run(["git", "worktree", "add", "-q", "-b", "task/1", str(wt), "main"], main)
        return main, wt

    return make


# ── the defect, and the fix ────────────────────────────────────────────────

def test_a_go_worktree_under_a_main_go_work_builds_once_it_has_its_own(checkout):
    main, wt = checkout()
    before = _go_build(wt)
    assert before.returncode != 0, before.stdout + before.stderr
    assert "go.work" in before.stderr

    assert go_support.ensure_go_work(wt) is True

    assert (wt / "go.work").exists()
    after = _go_build(wt)
    assert after.returncode == 0, after.stdout + after.stderr
    # The main checkout's file is not the one that was edited.
    assert (main / "go.work").read_text() == "go 1.21\n\nuse .\n"


def test_nested_modules_are_listed(checkout):
    main, wt = checkout()
    _module(wt / "examples" / "demo", "example.com/m/examples/demo")
    go_support.ensure_go_work(wt)
    text = (wt / "go.work").read_text()
    assert "\t.\n" in text
    assert "./test" in text
    assert "./examples/demo" in text
    nested = _go_build(wt / "test")
    assert nested.returncode == 0, nested.stdout + nested.stderr


def test_go_sets_the_go_line(checkout):
    main, wt = checkout()
    go_support.ensure_go_work(wt)
    first = (wt / "go.work").read_text().splitlines()[0]
    assert first.startswith("go 1.")


def test_it_runs_without_a_go_work_in_the_main_checkout_too(checkout):
    """Written in every Go worktree, so a main go.work added later needs no
    further step."""
    main, wt = checkout(go_work=False)
    assert go_support.ensure_go_work(wt) is True
    assert (wt / "go.work").exists()


# ── what it leaves alone ───────────────────────────────────────────────────

def test_an_existing_go_work_is_left_unchanged(checkout):
    main, wt = checkout()
    own = "go 1.21\n\nuse .\n\nreplace example.com/x => ../x\n"
    (wt / "go.work").write_text(own)
    assert go_support.ensure_go_work(wt) is False
    assert (wt / "go.work").read_text() == own


def test_a_non_go_worktree_gets_nothing(tmp_path, monkeypatch):
    wt = tmp_path / "wt"
    wt.mkdir()
    (wt / "README.md").write_text("not go\n")
    monkeypatch.setattr(go_support.subprocess, "run",
                        lambda *a, **k: pytest.fail("go must not be invoked"))
    assert go_support.ensure_go_work(wt) is False
    assert not (wt / "go.work").exists()


def test_modules_under_pruned_directories_do_not_count(tmp_path, monkeypatch):
    wt = tmp_path / "wt"
    for d in (".endless/sandbox", "vendor/example.com/dep", "testdata/mod"):
        _module(wt / d, f"example.com/{d}")
    monkeypatch.setattr(go_support.subprocess, "run",
                        lambda *a, **k: pytest.fail("go must not be invoked"))
    assert go_support.go_modules(wt) == []
    assert go_support.ensure_go_work(wt) is False


def test_pruned_directories_are_not_listed_beside_real_modules(checkout):
    main, wt = checkout()
    _module(wt / "vendor" / "dep", "example.com/dep")
    _module(wt / "testdata" / "fixture", "example.com/fixture")
    assert go_support.go_modules(wt) == [Path("."), Path("test")]
    go_support.ensure_go_work(wt)
    text = (wt / "go.work").read_text()
    assert "vendor" not in text and "testdata" not in text


def test_idempotent_at_every_start(checkout):
    main, wt = checkout()
    assert go_support.ensure_go_work(wt) is True
    written = (wt / "go.work").read_text()
    assert go_support.ensure_go_work(wt) is False
    assert (wt / "go.work").read_text() == written


# ── the .gitignore warning ─────────────────────────────────────────────────

def test_no_warning_when_gitignore_covers_go_work(checkout, capsys):
    main, wt = checkout(GITIGNORE_COVERING)
    go_support.ensure_go_work(wt)
    err = capsys.readouterr().err
    assert "wrote go.work" in err
    assert "not in .gitignore" not in err


def test_warning_when_gitignore_lacks_go_work(checkout, capsys):
    main, wt = checkout(GITIGNORE_LACKING)
    go_support.ensure_go_work(wt)
    err = capsys.readouterr().err
    assert "go.work is not in .gitignore" in err
    assert "go.work.sum" in err
    assert ".gitignore" in err


def test_an_anchored_pattern_counts_as_covered(checkout, capsys):
    main, wt = checkout("/go.work\n/go.work.sum\n")
    go_support.ensure_go_work(wt)
    assert "not in .gitignore" not in capsys.readouterr().err


def test_a_negated_pattern_does_not_count(checkout, capsys):
    main, wt = checkout("go.work*\n!go.work.sum\n")
    go_support.ensure_go_work(wt)
    assert "go.work is not in .gitignore" in capsys.readouterr().err


def test_info_exclude_does_not_count_and_is_never_written(checkout, capsys):
    """A hidden exclude would leave the owner unaware their .gitignore is
    missing the entry, so only the repository's .gitignore counts."""
    main, wt = checkout(GITIGNORE_LACKING)
    exclude = main / ".git" / "info" / "exclude"
    exclude.parent.mkdir(parents=True, exist_ok=True)
    before = "go.work\ngo.work.sum\n"
    exclude.write_text(before)
    go_support.ensure_go_work(wt)
    assert "go.work is not in .gitignore" in capsys.readouterr().err
    assert exclude.read_text() == before


def test_gitignore_covers_is_unknown_outside_a_repository(tmp_path):
    assert go_support.gitignore_covers(tmp_path, "go.work") is None


# ── failure, through the extension point ───────────────────────────────────

def test_a_missing_go_tool_is_reported_and_stops_nothing(
    checkout, monkeypatch, capsys,
):
    main, wt = checkout()
    monkeypatch.setattr(go_support.shutil, "which", lambda name: None)
    worktree_ext.run_extensions(wt)  # must not raise
    err = capsys.readouterr().err
    assert "worktree extension go-support failed" in err
    assert "`go` is not on PATH" in err
    assert not (wt / "go.work").exists()


def test_a_failing_go_command_names_the_command(checkout, monkeypatch, capsys):
    main, wt = checkout()
    # A module whose go.mod cannot be parsed makes `go work use` fail.
    (wt / "test" / "go.mod").write_text("this is not a go.mod\n")
    worktree_ext.run_extensions(wt)
    err = capsys.readouterr().err
    assert "worktree extension go-support failed" in err
    assert "go work use" in err
