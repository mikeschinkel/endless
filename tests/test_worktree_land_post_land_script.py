"""Tests for E-1799: the per-task post-land script `endless worktree land` runs
after the merge.

`_run_post_land_script` is a pure helper — given a main root, worktree path,
canonical id, merge sha, and base branch it discovers
`<main_root>/.endless/hooks/post-land/<task>.sh`, execs it (cwd = main root,
argv[1] = main root, the ENDLESS_* env set), and treats a non-zero exit as
non-fatal + loud. These tests drive it directly (no git/DB fixture); the
end-to-end call through a real land is covered by .endless/tasks/e-1799/verify.sh.

The final test pins the call SITE: `_run_post_land_script` must be invoked in
`land_worktree` after `_record_landing`/the Landed echo and before the reap
sweep, and must not be reached by the `record_only` early return.
"""

import inspect
import subprocess
from pathlib import Path

import pytest

from endless import worktree_cmd
from endless.worktree_cmd import _run_post_land_script, land_worktree


def _write_script(
    main_root: Path, canonical: str, body: str, *, executable: bool = True
) -> Path:
    d = main_root / ".endless" / "hooks" / "post-land"
    d.mkdir(parents=True, exist_ok=True)
    script = d / f"{canonical.lower()}.sh"
    script.write_text(body)
    if executable:
        script.chmod(0o755)
    return script


# ---------------------------------------------------------------------------
# discovery / no-op
# ---------------------------------------------------------------------------

def test_absent_script_is_a_silent_noop(tmp_path, capsys):
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    _run_post_land_script(worktree, main_root, "E-1799", "deadbeef", "main")
    out = capsys.readouterr()
    assert out.out == ""
    assert out.err == ""


def test_lowercase_id_discovery(tmp_path):
    # The canonical id is E-NNN; the file on disk is the lowercase e-NNN.sh.
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_script(
        main_root, "E-1799", f"#!/usr/bin/env bash\ntouch '{sentinel}'\n"
    )
    assert (main_root / ".endless/hooks/post-land/e-1799.sh").exists()
    _run_post_land_script(worktree, main_root, "E-1799", "sha", "main")
    assert sentinel.exists()


# ---------------------------------------------------------------------------
# invocation contract: cwd, argv, env
# ---------------------------------------------------------------------------

def test_runs_with_main_root_as_cwd_and_argv1(tmp_path, capsys):
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_script(
        main_root,
        "E-1799",
        "#!/usr/bin/env bash\n"
        f"printf 'ARG=%s\\n' \"$1\" > '{sentinel}'\n"
        f"printf 'CWD=%s\\n' \"$(pwd)\" >> '{sentinel}'\n",
    )
    _run_post_land_script(worktree, main_root, "E-1799", "sha", "main")
    # cwd may resolve symlinks (macOS /tmp), so compare against the realpath.
    real_main = main_root.resolve()
    assert sentinel.read_text() == f"ARG={main_root}\nCWD={real_main}\n"


def test_runs_with_endless_env_vars(tmp_path):
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_script(
        main_root,
        "E-1799",
        "#!/usr/bin/env bash\n"
        "{\n"
        '  printf "TASK=%s\\n" "$ENDLESS_TASK_ID"\n'
        '  printf "MERGE=%s\\n" "$ENDLESS_MERGE_SHA"\n'
        '  printf "WT=%s\\n" "$ENDLESS_WORKTREE_PATH"\n'
        '  printf "BASE=%s\\n" "$ENDLESS_BASE_BRANCH"\n'
        f"}} > '{sentinel}'\n",
    )
    _run_post_land_script(worktree, main_root, "E-1799", "abc123", "develop")
    assert sentinel.read_text() == (
        "TASK=E-1799\n"
        "MERGE=abc123\n"
        f"WT={worktree}\n"
        "BASE=develop\n"
    )


# ---------------------------------------------------------------------------
# failure handling: non-fatal + loud
# ---------------------------------------------------------------------------

def test_nonzero_exit_is_non_fatal_and_loud(tmp_path, capsys):
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    _write_script(main_root, "E-1799", "#!/usr/bin/env bash\nexit 5\n")
    # Must not raise — the land already happened.
    _run_post_land_script(worktree, main_root, "E-1799", "sha", "main")
    err = capsys.readouterr().err
    assert "post-land script exited 5" in err
    assert "e-1799.sh" in err
    assert str(main_root) in err            # cwd named
    # The re-run command is present so the human can finish the step by hand.
    assert "cd " in err and "e-1799.sh" in err
    assert "SUCCEEDED" in err               # land is not unwound


def test_non_executable_warns_and_does_not_run(tmp_path, capsys):
    main_root = tmp_path / "main"
    main_root.mkdir()
    worktree = tmp_path / "wt"
    worktree.mkdir()
    sentinel = tmp_path / "sentinel"
    _write_script(
        main_root,
        "E-1799",
        f"#!/usr/bin/env bash\ntouch '{sentinel}'\n",
        executable=False,
    )
    _run_post_land_script(worktree, main_root, "E-1799", "sha", "main")
    err = capsys.readouterr().err
    assert "not executable" in err
    assert "chmod +x" in err
    assert not sentinel.exists()


# ---------------------------------------------------------------------------
# call site
# ---------------------------------------------------------------------------

def test_call_site_is_after_record_landing_and_before_reap():
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_record = src.index("_record_landing(")
    i_post = src.index("_run_post_land_script(")
    i_reap = src.index("_reap_stale_worktrees(")
    # Ordering: record landing -> post-land script -> reap sweep.
    assert i_record < i_post < i_reap


def test_record_only_does_not_run_post_land_script():
    # The record_only branch returns before the success path, so the post-land
    # call must sit below that early return in the source.
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_record_only_return = src.index("_record_only_landing(")
    i_post = src.index("_run_post_land_script(")
    assert i_record_only_return < i_post


# ---------------------------------------------------------------------------
# end-to-end: a genuine land (real git rebase + ff-merge) fires the script
#
# Drives the real land_worktree() against real git repos, mocking only the
# DB/registry boundary (the same boundary test_worktree_land_record_landing.py
# mocks). The rebase, the ff-merge, the merge SHA, and the _run_post_land_script
# call are all real, so this proves the call site end-to-end.
# ---------------------------------------------------------------------------

CANON = "E-4242"


def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo + a `feat` worktree branched from it.

    The test commits its chosen post-land script onto `feat` (via
    `_commit_script`), then drives the real land_worktree() through
    `_drive_land`, which mocks only the DB/registry boundary. Yields the two
    repo paths.
    """
    main = tmp_path / "main"
    main.mkdir()
    _git(["git", "init", "-q", "-b", "main"], main)
    _git(["git", "config", "user.email", "t@t.t"], main)
    _git(["git", "config", "user.name", "t"], main)
    _git(["git", "config", "commit.gpgsign", "false"], main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)

    return {"main": main, "worktree": worktree}


def _drive_land(monkeypatch, main, worktree):
    """Run the real land_worktree() with the DB/registry boundary mocked."""
    recorded = {}

    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(
        worktree_cmd, "_enriched_list", lambda root: [{"_": 1}]
    )
    monkeypatch.setattr(
        worktree_cmd,
        "_branch_for_task",
        lambda rows, canonical: {
            "branch": "feat",
            "path": str(worktree),
            "companion": {"base_branch": "main"},
        },
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_endless_go", lambda wt, root: None
    )
    monkeypatch.setattr(
        worktree_cmd, "_dedup_worktree_verbs_against_main", lambda wt, root: False
    )
    monkeypatch.setattr(
        worktree_cmd, "_drop_orphan_amendable_commits", lambda wt, base: (0, "")
    )
    monkeypatch.setattr(
        worktree_cmd, "_ledger_touching_commits", lambda wt, base: []
    )
    monkeypatch.setattr(
        worktree_cmd, "_guard_modified_worktree", lambda wt, branch, canon: None
    )
    monkeypatch.setattr(
        worktree_cmd, "_resolve_project", lambda arg: (None, "p")
    )

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        recorded["merge_sha"] = merge_sha

    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)
    monkeypatch.setattr(worktree_cmd, "_reap_stale_worktrees", lambda root: None)

    land_worktree(CANON, dry_run=False)
    return recorded


def _commit_script(worktree, body, *, executable=True):
    d = worktree / ".endless" / "hooks" / "post-land"
    d.mkdir(parents=True, exist_ok=True)
    script = d / f"{CANON.lower()}.sh"
    script.write_text(body)
    script.chmod(0o755 if executable else 0o644)
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "add post-land script"], worktree)


def _head(repo):
    return subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


def test_e2e_land_runs_script_with_cwd_env_and_merge_sha(
    landable, monkeypatch, capsys, tmp_path
):
    main, worktree = landable["main"], landable["worktree"]
    sentinel = tmp_path / "sentinel"
    _commit_script(
        worktree,
        "#!/usr/bin/env bash\n"
        "{\n"
        '  printf "TASK=%s\\n" "$ENDLESS_TASK_ID"\n'
        '  printf "MERGE=%s\\n" "$ENDLESS_MERGE_SHA"\n'
        '  printf "BASE=%s\\n" "$ENDLESS_BASE_BRANCH"\n'
        '  printf "ARG=%s\\n" "$1"\n'
        '  printf "CWD=%s\\n" "$(pwd)"\n'
        f"}} > '{sentinel}'\n",
    )
    recorded = _drive_land(monkeypatch, main, worktree)

    # The ff-merge really advanced main to the script commit.
    merge_sha = recorded["merge_sha"]
    assert merge_sha == _head(main)

    body = sentinel.read_text()
    assert f"TASK={CANON}\n" in body
    assert f"MERGE={merge_sha}\n" in body
    assert "BASE=main\n" in body
    assert f"ARG={main}\n" in body
    assert f"CWD={main.resolve()}\n" in body


def test_e2e_land_absent_script_is_clean_noop(landable, monkeypatch, capsys):
    main, worktree = landable["main"], landable["worktree"]
    # A no-op commit so feat is one ahead of main without any post-land script.
    (worktree / "file.txt").write_text("hi\n")
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "unrelated change"], worktree)

    _drive_land(monkeypatch, main, worktree)
    err = capsys.readouterr().err
    assert "post-land" not in err  # no warning, no run line


def test_e2e_land_nonexecutable_script_warns_but_lands(
    landable, monkeypatch, capsys, tmp_path
):
    main, worktree = landable["main"], landable["worktree"]
    sentinel = tmp_path / "sentinel"
    _commit_script(
        worktree,
        f"#!/usr/bin/env bash\ntouch '{sentinel}'\n",
        executable=False,
    )
    _drive_land(monkeypatch, main, worktree)
    out = capsys.readouterr()
    # Land still succeeded (main advanced to the script commit).
    assert "Landed E-4242" in out.out
    assert "not executable" in out.err
    assert not sentinel.exists()  # skipped, never ran


def test_e2e_land_script_exit1_lands_and_reports_loudly(
    landable, monkeypatch, capsys
):
    main, worktree = landable["main"], landable["worktree"]
    _commit_script(
        worktree,
        "#!/usr/bin/env bash\necho 'boom from post-land' >&2\nexit 1\n",
    )
    _drive_land(monkeypatch, main, worktree)
    out = capsys.readouterr()
    assert "Landed E-4242" in out.out          # land NOT unwound
    assert "post-land script exited 1" in out.err
    assert "e-4242.sh" in out.err
    assert "SUCCEEDED" in out.err              # message says the land held
