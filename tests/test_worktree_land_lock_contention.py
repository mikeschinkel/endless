"""Tests for E-2174: a held index.lock must not fail a land.

`land_worktree` has run its whole body inside a LAND_MAX_RETRIES loop since
E-987, but Step 4's rebase raised `click.ClickException` directly on any
`CalledProcessError` — so the one failure mode that recurs most on a busy
project walked straight out of the retry loop it was standing inside.

The holder is normally Endless itself: `git status --porcelain` against every
worktree is what the session monitor repaints on, what the per-minute
worktree-unlanded job sweeps with, and what `worktree check` / `worktree sync`
shell out to. Worktree count is one per active task by design, so the collision
odds rise as a project gets busier — and the message the operator got,
"could not detach HEAD", read like repository damage.

Three layers:
  1. Unit — `_is_lock_contention`, and its disjointness from
     `_is_retryable_ff_merge_error`.
  2. Call site — every git call inside the loop consults it before deciding a
     failure is fatal, and the guard propagates contention rather than
     classifying it.
  3. End-to-end — a genuine land against a REAL held `index.lock`: it succeeds
     once the lock clears, terminates with a contention message when it never
     does, and is unchanged by either for a real conflict.
"""

import inspect
import subprocess
from pathlib import Path

import click
import pytest

from endless import worktree_cmd
from endless.worktree_cmd import (
    LAND_MAX_RETRIES,
    _guard_modified_worktree,
    _is_lock_contention,
    _is_retryable_ff_merge_error,
    _lock_contention_text,
    land_worktree,
)


# The land failure this task was filed for, verbatim (2026-09-23, E-2137).
E2174_STDERR = (
    "error: Unable to create "
    "'/p/.git/worktrees/e-2137/index.lock': File exists.\n"
    "\n"
    "Another git process seems to be running in this repository, e.g.\n"
    "an editor opened by 'git commit'. Please make sure all processes\n"
    "are terminated then try again. If it still fails, a git process\n"
    "may have crashed in this repository earlier:\n"
    "remove the file manually to continue.\n"
    "error: could not detach HEAD\n"
)


# ---------------------------------------------------------------------------
# unit: the predicate
# ---------------------------------------------------------------------------

@pytest.mark.parametrize(
    "err_text",
    [
        E2174_STDERR,
        # The lock file's own name — every git version prints this.
        "fatal: Unable to create '/p/.git/index.lock': File exists.",
        # The advisory sentence git adds when it recognizes a live holder.
        "Another git process seems to be running in this repository",
        # A worktree's lock lives under .git/worktrees/<name>/, not .git/.
        "error: Unable to create '/p/.git/worktrees/e-1/index.lock': File exists.",
    ],
)
def test_lock_contention_is_recognized(err_text):
    assert _is_lock_contention(err_text)


@pytest.mark.parametrize(
    "err_text",
    [
        "fatal: refusing to merge unrelated histories",
        "error: could not detach HEAD",   # the symptom alone is not the cause
        "CONFLICT (content): Merge conflict in a.txt",
        "",
    ],
)
def test_non_contention_is_not_recognized(err_text):
    assert not _is_lock_contention(err_text)


def test_lock_contention_text_returns_git_words_or_none():
    busy = subprocess.CalledProcessError(
        128, ["git"], output="", stderr=E2174_STDERR,
    )
    assert _lock_contention_text(busy) == E2174_STDERR

    other = subprocess.CalledProcessError(
        128, ["git"], output="", stderr="fatal: bad object HEAD\n",
    )
    assert _lock_contention_text(other) is None


# ---------------------------------------------------------------------------
# unit: the two predicates stay disjoint
# ---------------------------------------------------------------------------
# They answer "should land retry?" about different repositories: one that moved
# under the land, versus one that was merely busy. Folding them together would
# make a diverged branch and a live `git status` indistinguishable in every
# message that reports either.

@pytest.mark.parametrize("err_text", [
    E2174_STDERR,
    "fatal: Unable to create '/p/.git/index.lock': File exists.",
])
def test_contention_is_not_an_ff_merge_race(err_text):
    assert not _is_retryable_ff_merge_error(err_text)


@pytest.mark.parametrize("err_text", [
    "hint: Diverging branches can't be fast-forwarded",
    "fatal: Not possible to fast-forward, aborting.",
    "error: Your local changes would be overwritten by merge",
    "fatal: Updating would lose uncommitted changes",
])
def test_ff_merge_race_is_not_contention(err_text):
    assert _is_retryable_ff_merge_error(err_text)
    assert not _is_lock_contention(err_text)


# ---------------------------------------------------------------------------
# call site: the loop consults it everywhere, before classifying
# ---------------------------------------------------------------------------

def test_rebase_checks_contention_before_building_a_conflict_report():
    # A rebase that could not create index.lock never detached HEAD: there is
    # no conflict to classify and no rebase to abort. The check must therefore
    # come first, or land reports a conflict that did not happen.
    src = inspect.getsource(worktree_cmd.land_worktree)
    i_rebase = src.index('_git_run(["rebase", base_branch]')
    tail = src[i_rebase:]
    assert tail.index("_lock_contention_text(") < tail.index(
        "_rebase_failure_message("
    )


def test_every_git_call_in_the_loop_is_guarded():
    # Step 1 status, Step 3 auto-commit, Step 3.5 dedup, Step 3.7 orphan-drop
    # rebase, Step 3.8 guard, Step 4 rebase, Step 5 ff-merge. The bug was one
    # unguarded site inside an existing retry loop; the fix is the class.
    src = inspect.getsource(worktree_cmd.land_worktree)
    guards = src.count("_lock_contention_text(") + src.count("_is_lock_contention(")
    assert guards == 7, f"expected 7 guarded git calls in the loop, found {guards}"


def test_waiting_is_the_recovery_never_removing_the_lock():
    # The holder is a live process — the one observed in E-2174 was still
    # running at diagnosis and released the lock on its own moments later.
    # Removing another process's lock is how an index gets corrupted, so the
    # module must never name that file as something to delete.
    src = inspect.getsource(worktree_cmd)
    for line in src.splitlines():
        if "index.lock" not in line:
            continue
        assert not any(
            verb in line for verb in ("unlink", "os.remove", "rm(", "rmtree")
        ), f"land must wait a lock out, never remove it: {line.strip()}"


def test_guard_propagates_contention_rather_than_classifying_it(tmp_path, monkeypatch):
    # Step 3.8's own `git status` can lose the same race, one worktree over. It
    # says nothing about whether the worktree is modified — the status never
    # ran — so it must reach the retry loop, not become a ClickException.
    def busy(_root):
        raise subprocess.CalledProcessError(
            128, ["git", "status"], output="", stderr=E2174_STDERR,
        )

    monkeypatch.setattr(worktree_cmd, "_git_status_partition", busy)
    with pytest.raises(subprocess.CalledProcessError):
        _guard_modified_worktree(tmp_path, branch="feat", canonical="E-2174")


def test_guard_still_classifies_other_status_failures(tmp_path, monkeypatch):
    def broken(_root):
        raise subprocess.CalledProcessError(
            128, ["git", "status"], output="", stderr="fatal: not a git repository\n",
        )

    monkeypatch.setattr(worktree_cmd, "_git_status_partition", broken)
    with pytest.raises(click.ClickException) as ei:
        _guard_modified_worktree(tmp_path, branch="feat", canonical="E-2174")
    assert "git status in worktree failed" in ei.value.message


# ---------------------------------------------------------------------------
# end-to-end: a real land against a real held index.lock
# ---------------------------------------------------------------------------

CANON = "E-2174"


def _git(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


def _init_repo(path):
    path.mkdir(parents=True, exist_ok=True)
    _git(["git", "init", "-q", "-b", "main"], path)
    _git(["git", "config", "user.email", "t@t.t"], path)
    _git(["git", "config", "user.name", "t"], path)
    _git(["git", "config", "commit.gpgsign", "false"], path)


def _head(repo):
    return subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(repo),
        capture_output=True, text=True, check=True,
    ).stdout.strip()


@pytest.fixture
def landable(tmp_path):
    """A real `main` repo + a `feat` worktree with one commit to land.

    main also advances by one commit, so Step 4's rebase has real work to do
    and cannot be a no-op that succeeds with the index locked.
    """
    main = tmp_path / "main"
    _init_repo(main)
    (main / ".endless").mkdir()
    (main / ".endless" / "config.json").write_text('{"name": "p"}\n')
    (main / "README").write_text("x\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "init"], main)

    worktree = tmp_path / "wt"
    _git(["git", "worktree", "add", "-q", str(worktree), "-b", "feat", "main"], main)

    (main / "on-main.txt").write_text("m\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "main moves"], main)

    (worktree / "on-feat.txt").write_text("f\n")
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "branch work"], worktree)

    return {"main": main, "worktree": worktree}


def _worktree_index_lock(worktree: Path) -> Path:
    """The lock file a live `git status` in this worktree would hold.

    A linked worktree's index is in the repo's `.git/worktrees/<name>/`, not in
    `.git/` — resolved from git rather than typed out.
    """
    git_dir = subprocess.run(
        ["git", "rev-parse", "--absolute-git-dir"], cwd=str(worktree),
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    return Path(git_dir) / "index.lock"


def _patch_land(monkeypatch, main, worktree, recorded):
    """DB/registry-boundary mocks so land_worktree() runs on real git."""
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


def _no_sleep_backoff(monkeypatch, on_backoff=None):
    """Replace the backoff so tests do not spend it; count the waits."""
    waits = []

    def fake(attempt):
        waits.append(attempt)
        if on_backoff is not None:
            on_backoff(attempt)

    monkeypatch.setattr(worktree_cmd, "_lock_backoff", fake)
    return waits


def test_e2e_lock_that_clears_lands_with_no_operator_action(
    landable, monkeypatch, capsys
):
    # The E-2174 reproduction: the rebase loses the index lock once, the holder
    # exits, and the identical land then succeeds with no change to the branch.
    main, worktree = landable["main"], landable["worktree"]
    lock = _worktree_index_lock(worktree)
    lock.write_text("")
    assert lock.exists()

    waits = _no_sleep_backoff(monkeypatch, on_backoff=lambda a: lock.unlink())
    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)

    land_worktree(CANON, dry_run=False)   # must NOT raise

    assert waits == [1]                             # exactly one wait
    assert recorded["merge_sha"] == _head(main)     # the land happened
    landed = subprocess.run(
        ["git", "log", "--format=%s", "-1"], cwd=str(main),
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    assert landed == "branch work"
    assert not lock.exists()
    assert "Landed E-2174" in capsys.readouterr().out


def test_e2e_lock_that_never_clears_terminates_naming_contention(
    landable, monkeypatch
):
    # Still bounded by the retry cap, and the message must send the reader to a
    # busy repository rather than to a conflict that does not exist.
    main, worktree = landable["main"], landable["worktree"]
    _worktree_index_lock(worktree).write_text("")
    main_head_before = _head(main)

    waits = _no_sleep_backoff(monkeypatch)
    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)

    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert f"failed after {LAND_MAX_RETRIES} retries" in msg
    assert "index lock" in msg
    assert "NOT a conflict" in msg
    assert "Do NOT delete the lock file" in msg
    # It must not be mistaken for the auto-file race the loop already had.
    assert "appending to auto-files" not in msg
    assert waits == list(range(1, LAND_MAX_RETRIES + 1))
    assert "merge_sha" not in recorded             # nothing was recorded
    assert _head(main) == main_head_before         # and main never advanced


def test_e2e_real_conflict_is_unaffected(landable, monkeypatch):
    # A rebase that fails on conflicting CONTENT must still not retry, and must
    # still get the conflict report — contention handling sits in front of that
    # path, not in place of it.
    main, worktree = landable["main"], landable["worktree"]
    (main / "clash.txt").write_text("main side\n")
    _git(["git", "add", "-A"], main)
    _git(["git", "commit", "-q", "-m", "main clash"], main)
    (worktree / "clash.txt").write_text("feat side\n")
    _git(["git", "add", "-A"], worktree)
    _git(["git", "commit", "-q", "-m", "feat clash"], worktree)

    waits = _no_sleep_backoff(monkeypatch)
    recorded = {}
    _patch_land(monkeypatch, main, worktree, recorded)

    with pytest.raises(click.ClickException) as ei:
        land_worktree(CANON, dry_run=False)

    msg = ei.value.message
    assert waits == []                              # no retry, no backoff
    assert "index lock" not in msg
    assert "clash.txt" in msg
    assert "merge_sha" not in recorded
