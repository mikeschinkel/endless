"""Tests for E-1800: the post-land residue check `endless worktree land` runs
after the post-land script.

`_check_post_land_residue(main_root, canonical, ignored_before)` verifies a land
left no *untracked residue* under paths it newly un-ignored. Residue is the
intersection of two git classifications — `ignored_before` (ignored-and-present
on main under the pre-land rules, captured before the merge) ∩ the post-land
`_untracked_present_files(main_root)` (untracked-and-present under the now-merged
rules). Empty → silent; non-empty → a loud ClickException (non-fatal to the
already-advanced merge, but exits non-zero).

Three layers:
  1. Unit — `_check_post_land_residue` with the post-land untracked set mocked,
     plus the two `ls-files` parsers against a real throwaway repo.
  2. Call site — the pre-merge snapshot and the post-script check sit at the
     right places in `land_worktree` and are not reached by `record_only`.
  3. End-to-end — a genuine land (real rebase + ff-merge) over a repo whose
     branch un-ignores a path with on-disk cruft, exercising the plan's cases.
"""

import inspect
import subprocess
from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    _check_post_land_residue,
    _ignored_present_files,
    _untracked_present_files,
    land_worktree,
)


# ---------------------------------------------------------------------------
# unit: the I ∩ U computation and reporting
# ---------------------------------------------------------------------------

def test_disjoint_sets_are_a_silent_pass(tmp_path, monkeypatch, capsys):
    main = tmp_path / "main"
    main.mkdir()
    # Un-ignored-before vs untracked-after share nothing → no residue.
    monkeypatch.setattr(worktree_cmd, "_untracked_present_files", lambda r: {"a.txt"})
    _check_post_land_residue(main, "E-1800", {"b.txt"})
    out = capsys.readouterr()
    assert out.out == ""
    assert out.err == ""


def test_empty_ignored_before_is_the_common_noop(tmp_path, monkeypatch, capsys):
    # A land that un-ignored nothing: the snapshot is empty, so the
    # intersection is empty regardless of what is untracked after.
    main = tmp_path / "main"
    main.mkdir()
    monkeypatch.setattr(
        worktree_cmd, "_untracked_present_files", lambda r: {"x", "y", "z"}
    )
    _check_post_land_residue(main, "E-1800", set())
    assert capsys.readouterr().out == ""


def test_intersection_is_the_residue_and_raises(tmp_path, monkeypatch):
    main = tmp_path / "main"
    main.mkdir()
    # Post-land untracked set: two residual cruft files + one unrelated new file.
    monkeypatch.setattr(
        worktree_cmd,
        "_untracked_present_files",
        lambda r: {"cruft/a.txt", "cruft/b.txt", "keep.txt"},
    )
    with pytest.raises(click.ClickException) as ei:
        # ignored_before has the two cruft files + one path that is NOT untracked
        # after (e.g. still ignored, or tracked) so it must drop out.
        _check_post_land_residue(
            main, "E-1800", {"cruft/a.txt", "cruft/b.txt", "gone.txt"}
        )
    msg = ei.value.message
    # Exactly the intersection is reported.
    assert "cruft/a.txt" in msg
    assert "cruft/b.txt" in msg
    assert "keep.txt" not in msg   # untracked-after but never ignored-before
    assert "gone.txt" not in msg   # ignored-before but not untracked-after
    assert "E-1800" in msg
    assert "exits non-zero" in msg  # mirrors the "main advanced" surfacing


def test_no_script_shipped_note(tmp_path, monkeypatch):
    main = tmp_path / "main"
    main.mkdir()
    monkeypatch.setattr(worktree_cmd, "_untracked_present_files", lambda r: {"c.txt"})
    with pytest.raises(click.ClickException) as ei:
        _check_post_land_residue(main, "E-1800", {"c.txt"})
    assert "No post-land script was shipped" in ei.value.message


def test_existing_script_note(tmp_path, monkeypatch):
    main = tmp_path / "main"
    main.mkdir()
    d = main / ".endless" / "hooks" / "post-land"
    d.mkdir(parents=True)
    (d / "e-1800.sh").write_text("#!/usr/bin/env bash\n")
    monkeypatch.setattr(worktree_cmd, "_untracked_present_files", lambda r: {"c.txt"})
    with pytest.raises(click.ClickException) as ei:
        _check_post_land_residue(main, "E-1800", {"c.txt"})
    msg = ei.value.message
    assert "left these behind" in msg
    assert "e-1800.sh" in msg


def test_singular_vs_plural_noun(tmp_path, monkeypatch):
    main = tmp_path / "main"
    main.mkdir()
    monkeypatch.setattr(worktree_cmd, "_untracked_present_files", lambda r: {"one"})
    with pytest.raises(click.ClickException) as ei:
        _check_post_land_residue(main, "E-1800", {"one"})
    assert "1 path" in ei.value.message
    assert "1 paths" not in ei.value.message


# ---------------------------------------------------------------------------
# unit: the ls-files parsers over a real throwaway repo
# ---------------------------------------------------------------------------

def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _init_repo(path):
    path.mkdir(parents=True, exist_ok=True)
    _git(["git", "init", "-q", "-b", "main"], path)
    _git(["git", "config", "user.email", "t@t.t"], path)
    _git(["git", "config", "user.name", "t"], path)
    _git(["git", "config", "commit.gpgsign", "false"], path)


def test_ls_files_classifiers_split_ignored_from_untracked(tmp_path):
    repo = tmp_path / "repo"
    _init_repo(repo)
    (repo / ".gitignore").write_text("cruft/\n")
    _git(["git", "add", ".gitignore"], repo)
    _git(["git", "commit", "-q", "-m", "init"], repo)

    (repo / "cruft").mkdir()
    (repo / "cruft" / "x.txt").write_text("junk\n")   # ignored + present
    (repo / "new.txt").write_text("new\n")            # untracked, not ignored

    ignored = _ignored_present_files(repo)
    untracked = _untracked_present_files(repo)
    assert ignored == {"cruft/x.txt"}
    assert untracked == {"new.txt"}
    # The two classifications are disjoint by construction.
    assert ignored & untracked == set()


# ---------------------------------------------------------------------------
# call site: snapshot before the merge loop, check after the script, and the
# record_only early return reaches neither.
# ---------------------------------------------------------------------------

def test_snapshot_captured_before_the_merge_loop():
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_snap = src.index("_ignored_present_files(")
    i_loop = src.index("for attempt in range(")
    i_merge = src.index('"merge", "--ff-only"')
    assert i_snap < i_loop < i_merge


def test_check_runs_after_script_and_before_reap():
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_script = src.index("_run_post_land_script(")
    i_check = src.index("_check_post_land_residue(")
    i_reap = src.index("_reap_stale_worktrees(")
    assert i_script < i_check < i_reap


def test_record_only_reaches_neither_snapshot_nor_check():
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_record_only = src.index("_record_only_landing(")
    assert i_record_only < src.index("_ignored_present_files(")
    assert i_record_only < src.index("_check_post_land_residue(")


# ---------------------------------------------------------------------------
# end-to-end: a genuine land whose branch un-ignores a path with on-disk cruft
# ---------------------------------------------------------------------------

CANON = "E-4242"


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo (with a tracked .gitignore) + a `feat` worktree.

    main tracks `.gitignore` ignoring `cruft/`, so cruft files placed in main's
    working dir are ignored-and-present at land time. Each test rewrites
    `.gitignore` on `feat` (un-ignoring the path) and optionally ships a
    post-land script, then drives the real land.
    """
    main = tmp_path / "main"
    main.mkdir()
    _init_repo(main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / ".gitignore").write_text("cruft/\n")
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)
    return {"main": main, "worktree": worktree}


def _patch_land(monkeypatch, main, worktree, recorded):
    """Apply the DB/registry-boundary mocks so land_worktree() runs on real git."""
    monkeypatch.setattr("endless.config.default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: main)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda root: [{"_": 1}])
    monkeypatch.setattr(
        worktree_cmd,
        "_branch_for_task",
        lambda rows, canonical: {
            "branch": "feat",
            "path": str(worktree),
            "companion": {"base_branch": "main"},
        },
    )
    monkeypatch.setattr(worktree_cmd, "_resolve_land_endless_go", lambda wt, root: None)
    monkeypatch.setattr(
        worktree_cmd, "_dedup_worktree_verbs_against_main", lambda wt, root: False
    )
    monkeypatch.setattr(
        worktree_cmd, "_drop_orphan_amendable_commits", lambda wt, base: (0, "")
    )
    monkeypatch.setattr(worktree_cmd, "_ledger_touching_commits", lambda wt, base: [])
    monkeypatch.setattr(
        worktree_cmd, "_guard_modified_worktree", lambda wt, branch, canon: None
    )
    monkeypatch.setattr(worktree_cmd, "_resolve_project", lambda arg: (None, "p"))
    monkeypatch.setattr(worktree_cmd, "_reap_stale_worktrees", lambda root: None)

    def fake_record(item_id, proj_name, branch, base_branch, canonical,
                    merge_sha, endless_go_bin=None):
        recorded["merge_sha"] = merge_sha

    monkeypatch.setattr(worktree_cmd, "_record_landing", fake_record)


def _commit_on_feat(worktree, msg):
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", msg], worktree)


def _add_script(worktree, body):
    d = worktree / ".endless" / "hooks" / "post-land"
    d.mkdir(parents=True, exist_ok=True)
    script = d / f"{CANON.lower()}.sh"
    script.write_text(body)
    script.chmod(0o755)


def _head(repo):
    return subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


def _seed_cruft(main, names):
    (main / "cruft").mkdir(exist_ok=True)
    for n in names:
        (main / "cruft" / n).write_text("junk\n")


def test_e2e_script_removes_all_cruft_is_silent_pass(landable, monkeypatch, capsys):
    # Case 1: un-ignore a path with on-disk cruft + a script that removes it.
    main, worktree = landable["main"], landable["worktree"]
    _seed_cruft(main, ["a.txt", "b.txt"])
    (worktree / ".gitignore").write_text("")  # un-ignore cruft/
    _add_script(worktree, "#!/usr/bin/env bash\nrm -rf cruft\n")
    _commit_on_feat(worktree, "un-ignore cruft + cleanup script")

    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    land_worktree(CANON, dry_run=False)  # must NOT raise

    assert recorded["merge_sha"] == _head(main)  # land recorded
    assert not (main / "cruft").exists()          # script cleaned it
    err = capsys.readouterr().err
    assert "untracked residue" not in err


def test_e2e_script_removes_some_leaves_residue(landable, monkeypatch):
    # Case 2: script removes only some → residue = the leftovers; non-zero exit;
    # land still recorded.
    main, worktree = landable["main"], landable["worktree"]
    _seed_cruft(main, ["a.txt", "b.txt"])
    (worktree / ".gitignore").write_text("")
    _add_script(worktree, "#!/usr/bin/env bash\nrm -f cruft/a.txt\n")  # leaves b
    _commit_on_feat(worktree, "un-ignore cruft + partial cleanup")

    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert "cruft/b.txt" in msg
    assert "cruft/a.txt" not in msg          # the script removed it
    assert recorded["merge_sha"] == _head(main)  # land recorded despite raise
    assert (main / ".gitignore").read_text() == ""  # merge really advanced main


def test_e2e_no_script_leaves_all_cruft(landable, monkeypatch):
    # Case 3: NO post-land script → residue = all the cruft.
    main, worktree = landable["main"], landable["worktree"]
    _seed_cruft(main, ["a.txt", "b.txt"])
    (worktree / ".gitignore").write_text("")
    _commit_on_feat(worktree, "un-ignore cruft, no cleanup")

    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert "cruft/a.txt" in msg
    assert "cruft/b.txt" in msg
    assert "No post-land script was shipped" in msg
    assert recorded["merge_sha"] == _head(main)


def test_e2e_committed_tracked_content_is_not_residue(landable, monkeypatch, capsys):
    # Case 4: un-ignore a path whose content is committed (tracked) → no residue.
    main, worktree = landable["main"], landable["worktree"]
    # No on-disk cruft in main; instead feat un-ignores AND tracks the content.
    (worktree / ".gitignore").write_text("")
    (worktree / "cruft").mkdir()
    (worktree / "cruft" / "kept.txt").write_text("intentional\n")
    _commit_on_feat(worktree, "un-ignore cruft and track its content")

    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    land_worktree(CANON, dry_run=False)  # tracked content → passes silently

    assert recorded["merge_sha"] == _head(main)
    assert (main / "cruft" / "kept.txt").read_text() == "intentional\n"
    assert "untracked residue" not in capsys.readouterr().err


def test_e2e_land_that_unignores_nothing_is_a_noop(landable, monkeypatch, capsys):
    # Case 5: a land that un-ignores nothing → empty snapshot → no-op.
    main, worktree = landable["main"], landable["worktree"]
    # Cruft present but the .gitignore is unchanged, so nothing is un-ignored.
    _seed_cruft(main, ["a.txt"])
    (worktree / "file.txt").write_text("hi\n")
    _commit_on_feat(worktree, "unrelated change; cruft stays ignored")

    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)
    land_worktree(CANON, dry_run=False)  # must NOT raise

    assert recorded["merge_sha"] == _head(main)
    assert "untracked residue" not in capsys.readouterr().err
    # Cruft is still ignored on main after the land, so it is not residue.
    assert _ignored_present_files(main) == {"cruft/a.txt"}
