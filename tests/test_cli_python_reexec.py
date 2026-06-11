"""E-1513: bare endless from a self-dev worktree must re-exec into the
worktree's Python source under `--db sandbox`.

The global `endless` script is the editable install of main's source
(`uv tool install -e .` from main), so without a self-detect gate every
`endless --db sandbox ...` invocation from a worktree runs main's Python
against the worktree's sandbox DB — silently missing the branch's
Python-layer changes. Symmetric Python-layer fix to E-1510 (Go binary
self-detect).

These tests cover the two halves of the gate:

1. `_scan_db_choice` reads argv the same way DBAwareGroup.main does (last
   `--db` value wins, accepts both `--db X` and `--db=X`).
2. `worktree_python_reexec_target` returns the worktree path only when cwd
   is inside a sandbox-opted worktree AND the importing source isn't
   already inside that worktree (re-entrancy guard prevents loops).
3. `DBAwareGroup.main` actually calls `os.execvp` with the right argv when
   both halves agree, and stays in-process otherwise.
"""

from pathlib import Path

import pytest
from click.testing import CliRunner

from endless import cli, config
from endless.cli import _scan_db_choice, main


def _make_worktree(tmp_path: Path, sandbox: bool, task_id: str = "1513") -> Path:
    """Build <tmp>/proj/.endless/{config.json, worktrees/e-<id>} and return the
    worktree dir. config.json sets self_dev to `sandbox`."""
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    wt = endless / "worktrees" / f"e-{task_id}"
    wt.mkdir(parents=True)
    (endless / "config.json").write_text(
        '{"self_dev": %s}\n' % ("true" if sandbox else "false")
    )
    return wt


# --- _scan_db_choice ----------------------------------------------------------


def test_scan_db_choice_space_form():
    assert _scan_db_choice(["task", "show", "--db", "sandbox"]) == "sandbox"


def test_scan_db_choice_equals_form():
    assert _scan_db_choice(["task", "show", "--db=sandbox"]) == "sandbox"


def test_scan_db_choice_main():
    assert _scan_db_choice(["--db", "main", "task", "show"]) == "main"


def test_scan_db_choice_absent():
    assert _scan_db_choice(["task", "show"]) is None


def test_scan_db_choice_last_wins():
    # DBAwareGroup.main's argv loop overwrites db_value on each occurrence;
    # the pre-Click gate must agree so it doesn't re-exec on a value Click
    # would discard.
    assert _scan_db_choice(["--db", "main", "--db", "sandbox"]) == "sandbox"
    assert _scan_db_choice(["--db=sandbox", "--db=main"]) == "main"


def test_scan_db_choice_dangling_flag():
    # `--db` with no following value: matches DBAwareGroup's behaviour of
    # leaving db_value at its previous setting (here, None).
    assert _scan_db_choice(["task", "show", "--db"]) is None


# --- worktree_python_reexec_target -------------------------------------------


def test_reexec_target_returns_worktree_when_source_is_outside(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    # Source file lives somewhere completely outside the worktree (simulates
    # the global uv-tool install of main's source).
    external_src = tmp_path / "other" / "src" / "endless" / "cli.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    assert config.worktree_python_reexec_target(source_file=external_src) == wt.resolve()


def test_reexec_target_returns_none_when_source_is_inside(tmp_path, monkeypatch):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    # Source file IS the worktree's cli.py (simulates the post-re-exec
    # process). Re-entrancy guard fires.
    inside_src = wt / "src" / "endless" / "cli.py"
    inside_src.parent.mkdir(parents=True)
    inside_src.touch()
    assert config.worktree_python_reexec_target(source_file=inside_src) is None


def test_reexec_target_returns_none_when_cwd_not_in_worktree(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    external_src = tmp_path / "src" / "endless" / "cli.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    assert config.worktree_python_reexec_target(source_file=external_src) is None


def test_reexec_target_handles_slugged_worktree(tmp_path, monkeypatch):
    """Regression: slugged worktree dirs (e-NNN-slug) must resolve to the
    full dir, not `f"e-{task_id}"` reconstruction that loses the slug."""
    proj = tmp_path / "proj"
    endless = proj / ".endless"
    wt = endless / "worktrees" / "e-1513-add-foo"
    wt.mkdir(parents=True)
    (endless / "config.json").write_text('{"self_dev": true}\n')
    monkeypatch.chdir(wt)
    external_src = tmp_path / "other" / "src" / "endless" / "cli.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    target = config.worktree_python_reexec_target(source_file=external_src)
    assert target == wt.resolve()
    assert str(target).endswith("/e-1513-add-foo")


def test_reexec_target_returns_none_when_project_does_not_opt_in(tmp_path, monkeypatch):
    # Worktree path shape matches, but the project's config.json has
    # `self_dev: false` — `--db sandbox` would never apply here, so
    # the re-exec gate must not fire either.
    wt = _make_worktree(tmp_path, sandbox=False, task_id="1513")
    monkeypatch.chdir(wt)
    external_src = tmp_path / "other" / "cli.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    assert config.worktree_python_reexec_target(source_file=external_src) is None


# --- DBAwareGroup integration -------------------------------------------------


@pytest.fixture
def capture_execvp(monkeypatch):
    """Patch os.execvp to capture the argv it would have exec'd with instead
    of replacing the process. Lets tests assert what cli.DBAwareGroup.main
    *decided* without actually leaving the test process."""
    calls: list[tuple[str, list[str]]] = []

    def _fake_execvp(file, args):
        calls.append((file, list(args)))
        raise _ExecvpCalled(file, list(args))

    monkeypatch.setattr(cli.os, "execvp", _fake_execvp)
    return calls


class _ExecvpCalled(Exception):
    """Surfaced from the fake execvp so Click's CliRunner doesn't try to
    continue into argv parsing after the supposed exec."""

    def __init__(self, file, args):
        super().__init__(f"execvp({file!r}, {args!r})")
        self.file = file
        self.args = args


def test_dbaware_reexecs_when_db_sandbox_inside_worktree(
    tmp_path, monkeypatch, capture_execvp
):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    # Force cli's source_file probe to look "outside" the worktree by
    # pointing config.__file__ somewhere else. The helper resolves Path
    # objects, so a tmp file is enough.
    external_src = tmp_path / "outside" / "config.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    monkeypatch.setattr(config, "__file__", str(external_src))

    runner = CliRunner()
    result = runner.invoke(main, ["--db", "sandbox", "task", "show", "1"])

    assert len(capture_execvp) == 1, "expected exactly one os.execvp call"
    file, argv = capture_execvp[0]
    assert file == "uv"
    assert argv[:4] == ["uv", "run", "--directory", str(wt.resolve())]
    assert argv[4] == "endless"
    assert argv[5:] == ["--db", "sandbox", "task", "show", "1"]
    # The fake execvp raises so CliRunner records it as an exception:
    assert isinstance(result.exception, _ExecvpCalled)


def test_dbaware_does_not_reexec_under_db_main(
    tmp_path, monkeypatch, capture_execvp
):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    external_src = tmp_path / "outside" / "config.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    monkeypatch.setattr(config, "__file__", str(external_src))

    runner = CliRunner()
    runner.invoke(main, ["--db", "main", "--help"])
    assert capture_execvp == []


def test_dbaware_does_not_reexec_without_db_flag(
    tmp_path, monkeypatch, capture_execvp
):
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    external_src = tmp_path / "outside" / "config.py"
    external_src.parent.mkdir(parents=True)
    external_src.touch()
    monkeypatch.setattr(config, "__file__", str(external_src))

    runner = CliRunner()
    runner.invoke(main, ["--help"])
    assert capture_execvp == []


def test_dbaware_does_not_reexec_outside_worktree(
    tmp_path, monkeypatch, capture_execvp
):
    monkeypatch.chdir(tmp_path)  # not a worktree
    runner = CliRunner()
    # --db sandbox outside a worktree fails with a usage error in
    # apply_db_choice, but it must NOT trigger a re-exec.
    runner.invoke(main, ["--db", "sandbox", "task", "show", "1"])
    assert capture_execvp == []


def test_dbaware_does_not_reexec_when_already_in_worktree_source(
    tmp_path, monkeypatch, capture_execvp
):
    # Re-entrancy guard: simulate the post-uv-run process where config.py
    # lives INSIDE the worktree. The gate must not re-exec, or the spawned
    # child would loop forever.
    wt = _make_worktree(tmp_path, sandbox=True, task_id="1513")
    monkeypatch.chdir(wt)
    inside_src = wt / "src" / "endless" / "config.py"
    inside_src.parent.mkdir(parents=True)
    inside_src.touch()
    monkeypatch.setattr(config, "__file__", str(inside_src))

    runner = CliRunner()
    runner.invoke(main, ["--db", "sandbox", "task", "show", "1"])
    assert capture_execvp == []
