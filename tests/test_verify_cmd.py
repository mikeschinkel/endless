"""Tests for the `endless task verify` Python wrapper (E-1603 / E-1605).

`run_verify` is a thin shell to `endless-go verify`: it resolves the task id and
the binary, composes the argv, and propagates the child's exit code. These tests
pin that contract with the subprocess and resolvers stubbed — no real endless-go
runs — so they are the fast unit proof that the wrapper builds the command
correctly and never swallows the runner's exit status.

This module is also the reference `pytest/uv` check in E-1605's E-1603
verification suite (.endless/tasks/e-1603/verify.toml): the exemplar that
exercises the pytest first-class runner end to end.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import verify_cmd


class _FakeProc:
    """Stand-in for subprocess.CompletedProcess carrying just the return code."""

    def __init__(self, returncode: int):
        self.returncode = returncode


def _stub_run(monkeypatch, returncode: int = 0) -> list:
    """Stub subprocess.run to record each argv and return a fake process, and
    give the wrapper a deterministic binary. Returns the list of recorded argvs.
    """
    calls: list = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return _FakeProc(returncode)

    monkeypatch.setattr(subprocess, "run", fake_run)
    _stub_git_side(monkeypatch)
    return calls


def _stub_git_side(monkeypatch) -> None:
    """Give the wrapper a deterministic binary and take git out of the picture.

    These tests pin how the runner is invoked; the clean-tree gate and the
    recording of a passing run (E-2243) have their own tests below.
    """
    monkeypatch.setattr(verify_cmd, "_resolve_endless_go", lambda: "endless-go")
    monkeypatch.setattr(verify_cmd, "_require_clean_tree", lambda *a: None)
    monkeypatch.setattr(verify_cmd, "_head_sha", lambda *a: "abc1234")
    monkeypatch.setattr(verify_cmd, "_record_passing_run", lambda *a: None)


def test_explicit_id_builds_command_and_propagates_exit(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    with pytest.raises(SystemExit) as exc:
        verify_cmd.run_verify(1603, keep=False)
    assert exc.value.code == 0
    assert calls == [["endless-go", "verify", "E-1603"]]


def test_keep_flag_passed_through(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(1603, keep=True)
    assert calls == [["endless-go", "verify", "--keep", "E-1603"]]


def test_nonzero_exit_propagates(monkeypatch):
    _stub_run(monkeypatch, returncode=2)
    with pytest.raises(SystemExit) as exc:
        verify_cmd.run_verify(1603, keep=False)
    assert exc.value.code == 2


def test_none_id_resolves_active_task(monkeypatch):
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: 1758)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1758"]]


def test_none_id_falls_back_to_the_cwd_worktree(monkeypatch):
    """The session is the primary source; the checkout is the fallback.

    It needs no database, which is what makes a bare `endless task verify`
    work in a self-dev worktree with no --db and outside tmux (E-2023).
    """
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: None)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1889"]]


def test_session_wins_over_cwd(monkeypatch):
    """Standing in a foreign worktree must not retarget your own verify."""
    calls = _stub_run(monkeypatch, returncode=0)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: 1758)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1758"]]


def test_an_unreadable_session_is_not_the_answer(monkeypatch):
    """A refused database read falls through to the cwd rather than failing.

    Inside a self-dev worktree, reading the session's task without --db raises.
    That is a reason to try the other source, not to report no task.
    """
    calls = _stub_run(monkeypatch, returncode=0)

    def boom():
        raise click.ClickException("--db is required here")

    monkeypatch.setattr(verify_cmd, "_current_session_task_id", boom)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: 1889)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(None, keep=False)
    assert calls == [["endless-go", "verify", "E-1889"]]


def test_none_id_no_active_task_raises(monkeypatch):
    _stub_run(monkeypatch)
    monkeypatch.setattr(verify_cmd, "_current_session_task_id", lambda: None)
    monkeypatch.setattr(verify_cmd, "_cwd_task_id", lambda: None)
    with pytest.raises(click.ClickException):
        verify_cmd.run_verify(None, keep=False)


def test_cwd_task_id_reads_the_worktree_path():
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/e-1889")) == 1889
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/e-1889/src/x")) == 1889
    assert verify_cmd._cwd_task_id(Path("/p/src")) is None
    assert verify_cmd._cwd_task_id(Path("/p/.endless/worktrees/scratch")) is None


def test_runs_in_the_tasks_worktree(monkeypatch, tmp_path):
    """A suite proves the CANDIDATE tree, so it runs there — not in main.

    Without this, asking for a task from the main checkout would discover
    main's copy of the suite and run it against code that has not landed.
    """
    worktree = tmp_path / ".endless" / "worktrees" / "e-1889"
    worktree.mkdir(parents=True)
    monkeypatch.setattr(verify_cmd, "_main_checkout", lambda: tmp_path)

    cwds: list = []

    def fake_run(cmd, **kwargs):
        cwds.append(kwargs.get("cwd"))
        return _FakeProc(0)

    monkeypatch.setattr(subprocess, "run", fake_run)
    _stub_git_side(monkeypatch)
    with pytest.raises(SystemExit):
        verify_cmd.run_verify(1889, keep=False)
    assert cwds == [str(worktree)]


def test_missing_worktree_inherits_cwd(monkeypatch, tmp_path):
    """A reaped worktree, or a project not using them, still verifies."""
    monkeypatch.setattr(verify_cmd, "_main_checkout", lambda: tmp_path)
    assert verify_cmd._run_dir(1889) is None


# ─── E-2243: the clean-tree gate and the record of a passing run ─────────────
#
# These run against a real git repository laid out the way Endless lays one
# out — a main checkout with the task's worktree at .endless/worktrees/e-NNN —
# and a stand-in for endless-go. The stand-in plays the runner (writing one
# report per run into a temp cache dir, then exiting with the chosen code),
# answers --report-dir, and commits the report through the same `git add` +
# `git commit -o` the real verb ends in. The real verb's own guarantees (always
# a new commit, main checkout only) are pinned in internal/events.

TASK = 77
TASK_ID = f"E-{TASK}"


def _git(cwd, *args) -> str:
    return subprocess.run(
        ["git", "-C", str(cwd), *args], check=True, capture_output=True,
        text=True, env=verify_cmd.sanitized_git_env(),
    ).stdout.strip()


@pytest.fixture
def project(tmp_path, monkeypatch):
    """A main checkout plus TASK's worktree; cwd is the worktree."""
    main = tmp_path / "proj"
    main.mkdir()
    _git(main, "init", "-q", "-b", "main")
    _git(main, "config", "user.email", "t@example.com")
    _git(main, "config", "user.name", "T")
    _git(main, "config", "commit.gpgsign", "false")
    (main / ".gitignore").write_text(".endless/worktrees/\n")
    (main / "README").write_text("hi\n")
    (main / ".endless").mkdir()
    (main / ".endless" / "verbs.jsonl").write_text('{"verb": "fix"}\n')
    _git(main, "add", ".")
    _git(main, "commit", "-q", "-m", "init")
    wt = main / ".endless" / "worktrees" / f"e-{TASK}"
    _git(main, "worktree", "add", "-q", "-b", f"task/{TASK}", str(wt))
    monkeypatch.chdir(wt)
    cache = tmp_path / "cache" / TASK_ID
    return {"main": main, "wt": wt, "cache": cache}


def _fake_go(monkeypatch, project, *, exit_codes, commit_ok=True):
    """Stand in for endless-go; each runner call pops the next exit code."""
    real_run = subprocess.run
    codes = list(exit_codes)
    clock = iter(range(100, 200))

    def fake_run(cmd, **kwargs):
        if cmd[0] != "endless-go":
            return real_run(cmd, **kwargs)
        if cmd[1:3] == ["verify", "--report-dir"]:
            return subprocess.CompletedProcess(cmd, 0, f"{project['cache']}\n", "")
        if cmd[1] == "verify":
            project["cache"].mkdir(parents=True, exist_ok=True)
            name = f"20261006T120{next(clock)}.000000Z.ctrf.json"
            (project["cache"] / name).write_text('{"results": {}}\n')
            return subprocess.CompletedProcess(cmd, codes.pop(0))
        if cmd[1:3] == ["event", "commit-verify-report"]:
            if not commit_ok:
                return subprocess.CompletedProcess(cmd, 1, "", "git commit: refused\n")
            root = cmd[cmd.index("--project-root") + 1]
            rel = cmd[cmd.index("--path") + 1]
            task = cmd[cmd.index("--task") + 1]
            _git(root, "add", "--", rel)
            _git(root, "commit", "-q", "-o", rel, "-m",
                 f"Endless: verify {task} {Path(rel).name}")
            return subprocess.CompletedProcess(cmd, 0, "", "")
        raise AssertionError(f"unexpected endless-go call: {cmd}")

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(verify_cmd, "_resolve_endless_go", lambda: "endless-go")


def _verify() -> int:
    with pytest.raises(SystemExit) as exc:
        verify_cmd.run_verify(TASK, keep=False)
    return exc.value.code


def _recorded(main) -> list[str]:
    return sorted(p.name for p in (main / ".endless" / "tasks" / f"e-{TASK}")
                  .glob("verify-*.ctrf.json")) if (main / ".endless" / "tasks").exists() else []


def test_dirty_worktree_is_refused_before_anything_runs(project, monkeypatch):
    (project["wt"] / "new.py").write_text("x = 1\n")
    _fake_go(monkeypatch, project, exit_codes=[])  # any runner call would fail
    with pytest.raises(click.ClickException) as exc:
        verify_cmd.run_verify(TASK, keep=False)
    assert "new.py" in exc.value.format_message() + str(getattr(exc.value, "detail", ""))
    assert not project["cache"].exists()


def test_endless_managed_change_is_not_refused(project, monkeypatch):
    with (project["wt"] / ".endless" / "verbs.jsonl").open("a") as f:
        f.write('{"verb": "add"}\n')
    _fake_go(monkeypatch, project, exit_codes=[1])
    assert _verify() == 1


def test_failing_runs_commit_nothing_and_accumulate_in_cache(project, monkeypatch, capsys):
    head = _git(project["main"], "rev-parse", "HEAD")
    _fake_go(monkeypatch, project, exit_codes=[1, 1])
    assert _verify() == 1
    assert _verify() == 1
    assert _git(project["main"], "rev-parse", "HEAD") == head
    assert len(list(project["cache"].glob("*.ctrf.json"))) == 2
    assert "CTRF:" not in capsys.readouterr().out  # the runner prints its own


def test_passing_run_commits_one_report_and_empties_the_cache(project, monkeypatch, capsys):
    sha = _git(project["wt"], "rev-parse", "--short", "HEAD")
    head = _git(project["main"], "rev-parse", "HEAD")
    _fake_go(monkeypatch, project, exit_codes=[1, 1, 0])
    _verify(), _verify()
    capsys.readouterr()

    assert _verify() == 0

    recorded = _recorded(project["main"])
    assert len(recorded) == 1
    name = recorded[0]
    assert name.startswith("verify-20261006T") and name.endswith(f"Z-{sha}.ctrf.json")
    rel = f".endless/tasks/e-{TASK}/{name}"
    assert _git(project["main"], "rev-parse", "HEAD~1") == head
    assert _git(project["main"], "show", "--name-only", "--format=", "HEAD") == rel
    assert _git(project["main"], "log", "-1", "--format=%s") == f"Endless: verify {TASK_ID} {name}"
    assert list(project["cache"].iterdir()) == []
    out = capsys.readouterr().out
    assert out.count("CTRF:") == 1
    assert out.strip().endswith(rel)


def test_commit_failure_fails_the_command_and_keeps_the_cache(project, monkeypatch):
    _fake_go(monkeypatch, project, exit_codes=[1, 0], commit_ok=False)
    _verify()
    with pytest.raises(click.ClickException) as exc:
        verify_cmd.run_verify(TASK, keep=False)
    assert exc.value.exit_code != 0
    # The failed run's report stays for diagnosis; the passing one sits on
    # main, uncommitted, where the refusal says it is.
    assert len(list(project["cache"].glob("*.ctrf.json"))) == 1
    recorded = _recorded(project["main"])
    assert len(recorded) == 1
    assert recorded[0] in exc.value.format_message()
    assert _git(project["main"], "status", "--porcelain", "--", ".endless/tasks")
