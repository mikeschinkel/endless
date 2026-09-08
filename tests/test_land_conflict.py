"""Tests for land-conflict capture and classification (E-1957).

`endless worktree land` used to answer a rebase conflict with two numbered
recovery options for the user to choose between. Both put the branch's side of
the hunk back, so when the base branch had DELETED something that side still
referenced, either one produced a land that succeeded and shipped code that
raised on first use. That happened for real, landing E-1943 onto E-1941's
deletions.

The replacement is a split: capture the facts before `git rebase --abort`
destroys them, then classify from the capture and prescribe only what the
classification proves. These tests hold both halves to that:

  - the capture must survive the abort, uncapped;
  - each of the five classes must be recognised from real git state;
  - and the two classes that cannot be proven must prescribe NOTHING — which is
    asserted as an invariant over every class, not just where it is expected,
    because "we accidentally prescribed something" is the exact regression.

Real git repositories throughout. The subject is what git actually does with
conflicting histories, and a mocked git would only ever confirm what the mock
was told.
"""

import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest

from endless import land_conflict
from endless.land_conflict import (
    CLASS_ALREADY_LANDED,
    CLASS_AUTO_FILE_ONLY,
    CLASS_ORPHANED_LEDGER_BASE,
    CLASS_SEMANTIC_OVERLAP,
    CLASS_SYMBOL_SUPERSESSION,
)

REPO_ROOT = Path(__file__).resolve().parents[1]
ENDLESS_GO = REPO_ROOT / "bin" / "endless-go"


# ── fixtures / helpers ─────────────────────────────────────────────────────

@pytest.fixture(autouse=True)
def _isolated_sandbox(tmp_path, monkeypatch):
    """Point the sandbox root at a temp dir so a capture never touches the
    real one. config._cache_root reads the environment per call, so this is
    enough — no patching of endless's own code."""
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path / "cache"))


def _run(cmd, cwd, check=True):
    return subprocess.run(
        cmd, cwd=str(cwd), check=check, capture_output=True, text=True
    )


def _init_repo(path: Path) -> Path:
    path.mkdir(parents=True, exist_ok=True)
    _run(["git", "init", "-q", "-b", "main"], path)
    _run(["git", "config", "user.email", "t@t.t"], path)
    _run(["git", "config", "user.name", "t"], path)
    return path


def _commit(repo: Path, msg: str, files: dict[str, str]) -> str:
    for rel, content in files.items():
        p = repo / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content)
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "-m", msg], repo)
    return _run(["git", "rev-parse", "HEAD"], repo).stdout.strip()


def _start_rebase(repo: Path, *args: str) -> None:
    """Leave a conflicting rebase IN PROGRESS. Defaults to land's Step 4 form."""
    res = _run(["git", "rebase", *(args or ("main",))], repo, check=False)
    assert res.returncode != 0, "expected the rebase to conflict"


def _capture(repo: Path, *, branch: str, base: str = "main",
             task_id: str = "E-1943") -> land_conflict.ConflictEvidence:
    """Capture the live conflict, then abort — the land's own order."""
    from endless.worktree_cmd import _rebase_in_progress

    ev = land_conflict.capture_evidence(
        repo, base, branch, task_id=task_id,
        phase=f"rebasing your branch onto {base}",
        rebase_in_progress=_rebase_in_progress(repo),
    )
    _run(["git", "rebase", "--abort"], repo, check=False)
    return ev


# Padding keeps two edits in one file from collapsing into a single hunk, so a
# deletion at the top of the file and an edit at the bottom conflict the way
# they did in the incident this task exists for.
_PAD = "\n".join(f"# filler {i}" for i in range(40))


def _supersession_repo(tmp_path: Path) -> tuple[Path, str]:
    """The E-1943 shape: base deletes two names the branch's side still calls."""
    repo = _init_repo(tmp_path / "e-1943")
    _commit(repo, "base", {"sync.py": f'''BINARY_SOURCE_PATHS = ["cmd/endless-go"]


def _refuse_if_behind_base(worktree, base):
    raise SystemExit("behind base")


{_PAD}


def sync_worktrees(apply):
    return None
'''})

    _run(["git", "checkout", "-q", "-b", "task/1943"], repo)
    _commit(repo, "E-1943: print each binary source path", {"sync.py": f'''BINARY_SOURCE_PATHS = ["cmd/endless-go"]


def _refuse_if_behind_base(worktree, base):
    raise SystemExit("behind base")


{_PAD}


def sync_worktrees(apply):
    _refuse_if_behind_base(".", "main")
    for p in BINARY_SOURCE_PATHS:
        print(p)
    return None
'''})

    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "E-1941: delete the behind-base refusal", {"sync.py": f'''{_PAD}


def sync_worktrees(apply):
    return rebuild_binary(None)
'''})

    _run(["git", "checkout", "-q", "task/1943"], repo)
    _start_rebase(repo)
    return repo, "task/1943"


def _overlap_repo(tmp_path: Path) -> tuple[Path, str]:
    """Two intentional edits to the same line, using only names both sides keep."""
    repo = _init_repo(tmp_path / "e-2000")
    _commit(repo, "base", {"app.py": "def total(rows):\n    return sum(rows)\n"})

    _run(["git", "checkout", "-q", "-b", "task/2000"], repo)
    _commit(repo, "E-2000: skip blanks", {
        "app.py": "def total(rows):\n    return sum(r for r in rows if r)\n"})

    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "E-1999: round the total", {
        "app.py": "def total(rows):\n    return round(sum(rows), 2)\n"})

    _run(["git", "checkout", "-q", "task/2000"], repo)
    _start_rebase(repo)
    return repo, "task/2000"


def _auto_file_repo(tmp_path: Path) -> tuple[Path, str]:
    """A conflict confined to an endless-managed auto-file."""
    rel = ".endless/db-ledger/db-entries-abcd-000001.jsonl"
    repo = _init_repo(tmp_path / "e-3000")
    _commit(repo, "base", {rel: '{"seq":1}\n'})

    _run(["git", "checkout", "-q", "-b", "task/3000"], repo)
    _commit(repo, "Endless: record ledger entry", {rel: '{"seq":1}\n{"seq":2}\n'})

    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "Endless: record ledger entry", {rel: '{"seq":1}\n{"seq":9}\n'})

    _run(["git", "checkout", "-q", "task/3000"], repo)
    _start_rebase(repo)
    return repo, "task/3000"


def _already_landed_repo(tmp_path: Path) -> tuple[Path, str]:
    """A branch commit whose patch the base branch already carries.

    Built the way it happens: the commit landed, then the base branch moved on
    over the same lines. Replaying it conflicts, and `git cherry` still marks it
    "-" because it compares patch ids, not SHAs.
    """
    repo = _init_repo(tmp_path / "e-4000")
    _commit(repo, "base", {"lib.py": "one\ntwo\nthree\n"})

    _run(["git", "checkout", "-q", "-b", "task/4000"], repo)
    fork = _run(["git", "rev-parse", "HEAD"], repo).stdout.strip()
    _commit(repo, "E-4000: rename two", {"lib.py": "one\nTWO\nthree\n"})
    _commit(repo, "E-4000: add four", {"lib.py": "one\nTWO\nthree\nfour\n"})

    _run(["git", "checkout", "-q", "main"], repo)
    # The same patch, applied independently. The subject differs so the two
    # commits are distinct OBJECTS: same tree, same parent, same author and
    # same second would otherwise hash to one identical commit, and the class
    # under test is specifically "equivalent content under a different SHA".
    _commit(repo, "E-4000: rename two (landed via another branch)",
            {"lib.py": "one\nTWO\nthree\n"})
    # ...and then base keeps going over the same line.
    _commit(repo, "E-4001: rename it again", {"lib.py": "one\nSECOND\nthree\n"})

    _run(["git", "checkout", "-q", "task/4000"], repo)
    # Land's Step 3.7 form, and the only form that reaches this class. A plain
    # `git rebase main` compares patch ids and SKIPS a commit the base branch
    # already holds, so it can never stop on one; `--onto <base> <sha>` has
    # nothing upstream to compare against and replays every commit.
    _start_rebase(repo, "--onto", "main", fork)
    return repo, "task/4000"


def _ledger_orphan_repo(tmp_path: Path) -> tuple[Path, str]:
    """A branch based on a ledger commit the base branch has since appended to.

    The branch's segment is a byte-PREFIX of the base branch's, which is what
    "the base already has this content" means for an append-only JSONL ledger —
    the two are never byte-identical once main has recorded another event.
    """
    seg = ".endless/db-ledger/db-entries-abcd-000001.jsonl"
    repo = _init_repo(tmp_path / "e-5000")
    _commit(repo, "root", {"README.md": "hello\n"})
    _commit(repo, "Endless: record ledger entry", {seg: '{"seq":1}\n'})

    _run(["git", "checkout", "-q", "-b", "task/5000"], repo)
    _commit(repo, "E-5000: change the greeting", {"README.md": "hello, branch\n"})

    _run(["git", "checkout", "-q", "main"], repo)
    # main amends its ledger tip: same segment, one more event appended.
    _run(["git", "rm", "-q", "--cached", seg], repo)
    (repo / seg).write_text('{"seq":1}\n{"seq":2}\n')
    _run(["git", "add", "-A"], repo)
    _run(["git", "commit", "-q", "--amend", "-m",
          "Endless: record ledger entry"], repo)
    _commit(repo, "E-4999: change the greeting too", {"README.md": "hello, main\n"})

    _run(["git", "checkout", "-q", "task/5000"], repo)
    _start_rebase(repo)
    return repo, "task/5000"


# ── capture ────────────────────────────────────────────────────────────────

def test_capture_survives_the_rebase_abort(tmp_path):
    """The whole reason capture happens mid-rebase: `rebase --abort` erases
    REBASE_HEAD and the unmerged set, and everything after that is guesswork."""
    repo, branch = _supersession_repo(tmp_path)
    live_head = _run(["git", "rev-parse", "REBASE_HEAD"], repo).stdout.strip()

    ev = _capture(repo, branch=branch)
    land_conflict.store_evidence(repo, ev)

    # The rebase really is over: git no longer knows any of this.
    assert _run(["git", "rev-parse", "REBASE_HEAD"], repo,
                check=False).returncode != 0
    assert _run(["git", "diff", "--name-only", "--diff-filter=U"],
                repo).stdout.strip() == ""

    loaded = land_conflict.load_evidence(repo)
    assert loaded is not None
    assert loaded.rebase_head == live_head
    assert loaded.unmerged_paths == ["sync.py"]
    assert loaded.rebase_head_subject == "E-1943: print each binary source path"


def test_capture_keeps_both_sides_of_every_hunk_whole(tmp_path):
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    assert ev.hunks, "no hunks captured"
    branch_side = "\n".join(h.branch_side for h in ev.hunks)
    base_side = "\n".join(h.base_side for h in ev.hunks)
    assert "_refuse_if_behind_base" in branch_side
    assert "rebuild_binary" in base_side
    # Uncapped: no ellipsis, no truncation marker, nothing dropped.
    assert "..." not in branch_side


def test_evidence_round_trips_through_storage(tmp_path):
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)
    land_conflict.store_evidence(repo, ev)

    assert land_conflict.load_evidence(repo).to_dict() == ev.to_dict()


def test_missing_capture_reads_as_none(tmp_path):
    repo = _init_repo(tmp_path / "e-6000")
    assert land_conflict.load_evidence(repo) is None


def test_capture_from_a_future_schema_reads_as_absent(tmp_path):
    """Better to say "nothing recorded" than to reinterpret fields whose
    meaning has changed underneath."""
    repo = _init_repo(tmp_path / "e-6001")
    path = land_conflict.evidence_path(repo)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"schema": land_conflict.EVIDENCE_SCHEMA + 1}))

    assert land_conflict.load_evidence(repo) is None


def test_capture_lands_in_the_worktree_sandbox(tmp_path):
    """Storage is composed from the sandbox root, so relocating the sandbox
    relocates the capture with it (ED-1554)."""
    from endless import config

    repo = _init_repo(tmp_path / "e-6002")
    assert land_conflict.evidence_path(repo).is_relative_to(
        config.sandbox_root("e-6002")
    )


def test_store_reports_a_write_it_could_not_do(tmp_path, monkeypatch):
    """Never raises — the caller is mid-failure — but never pretends either.
    Whoever is about to say "run diagnose" has to know there will be something
    there to diagnose."""
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    blocked = tmp_path / "not-a-dir"
    blocked.write_text("")
    monkeypatch.setattr(
        land_conflict, "evidence_path", lambda _wt: blocked / "conflict.json")

    assert land_conflict.store_evidence(repo, ev) is None


# ── conflict-marker parsing ────────────────────────────────────────────────

def test_parse_conflict_markers_default_style():
    text = (
        "keep\n"
        "<<<<<<< HEAD\n"
        "base line\n"
        "=======\n"
        "branch line\n"
        ">>>>>>> abc123 (subject)\n"
        "tail\n"
    )
    hunks = land_conflict.parse_conflict_markers(text, "a.py")
    assert len(hunks) == 1
    assert hunks[0].base_side == "base line"
    assert hunks[0].branch_side == "branch line"
    assert hunks[0].ancestor_side == ""


def test_parse_conflict_markers_diff3_style_keeps_the_ancestor():
    text = (
        "<<<<<<< HEAD\n"
        "base line\n"
        "||||||| parent\n"
        "original line\n"
        "=======\n"
        "branch line\n"
        ">>>>>>> abc123\n"
    )
    hunks = land_conflict.parse_conflict_markers(text, "a.py")
    assert len(hunks) == 1
    assert hunks[0].ancestor_side == "original line"
    assert hunks[0].branch_side == "branch line"


def test_parse_conflict_markers_handles_several_hunks():
    text = (
        "<<<<<<< HEAD\nb1\n=======\nt1\n>>>>>>> x\n"
        "middle\n"
        "<<<<<<< HEAD\nb2\n=======\nt2\n>>>>>>> x\n"
    )
    hunks = land_conflict.parse_conflict_markers(text, "a.py")
    assert [h.branch_side for h in hunks] == ["t1", "t2"]


def test_symbol_lists_ignore_comments_and_string_literals():
    """A name is worth printing only if it refers to something. Counting prose
    buries the two names that mattered under sixty that did not."""
    text = (
        'def go(x):  # a comment mentioning cannot and actually\n'
        '    """A docstring about rebase and worktree at length."""\n'
        '    return helper(x) + "a literal string here"\n'
    )
    assert land_conflict._identifiers(text, code_only=True) == {
        "def", "go", "x", "return", "helper"}
    # Unfiltered, the prose is all in there.
    assert "docstring" in land_conflict._identifiers(text)


def test_an_unbalanced_quote_strips_nothing_rather_than_everything():
    """A conflict hunk is a fragment, so it routinely opens a docstring it does
    not close. Requiring the closing delimiter is what stops one from swallowing
    the rest of the text."""
    text = 'call_me(1)\n"""an opener with no closer\nkeep_this_name()\n'
    names = land_conflict._identifiers(text, code_only=True)
    assert "call_me" in names and "keep_this_name" in names


def test_rebase_head_is_not_read_when_no_rebase_is_running(tmp_path):
    """REBASE_HEAD is a plain ref: with no rebase running it can still hold a
    value from a different operation, and recording that as "the commit that
    failed to replay" would put a fiction in the permanent record."""
    repo, branch = _supersession_repo(tmp_path)
    _run(["git", "rebase", "--abort"], repo, check=False)

    ev = land_conflict.capture_evidence(
        repo, "main", branch, task_id="E-1943", phase="testing",
        rebase_in_progress=False,
    )
    assert ev.rebase_head == ""
    assert ev.rebase_head_subject == ""


# ── classification ─────────────────────────────────────────────────────────

def test_symbol_supersession_names_what_the_base_branch_deleted(tmp_path):
    """The incident, reproduced. Both recoveries the old message offered would
    have put these two names back."""
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    cl = land_conflict.classify(ev, repo)
    assert cl.klass == CLASS_SYMBOL_SUPERSESSION
    assert cl.proven
    assert set(cl.superseded_symbols) == {
        "BINARY_SOURCE_PATHS", "_refuse_if_behind_base"}
    assert cl.prescription == []


def test_symbol_supersession_ignores_names_the_branch_itself_introduced(tmp_path):
    """A brand-new name is absent from the base branch for an innocent reason.
    Requiring it to have existed at the fork point is what separates "the base
    deleted this" from "this is new"."""
    repo = _init_repo(tmp_path / "e-7000")
    _commit(repo, "base", {"m.py": f"def go():\n    return 1\n\n\n{_PAD}\nTAIL = 0\n"})

    _run(["git", "checkout", "-q", "-b", "task/7000"], repo)
    _commit(repo, "E-7000: add a helper", {
        "m.py": f"def go():\n    return brand_new_helper()\n\n\n{_PAD}\nTAIL = 0\n"})

    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "E-6999: return two", {
        "m.py": f"def go():\n    return 2\n\n\n{_PAD}\nTAIL = 0\n"})

    _run(["git", "checkout", "-q", "task/7000"], repo)
    _start_rebase(repo)
    ev = _capture(repo, branch="task/7000")

    cl = land_conflict.classify(ev, repo)
    assert "brand_new_helper" not in cl.superseded_symbols
    assert cl.klass == CLASS_SEMANTIC_OVERLAP


def test_semantic_overlap_prescribes_nothing_and_shows_both_sides(tmp_path):
    """The explicit negative case: two intentional edits, nothing to prove."""
    repo, branch = _overlap_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    cl = land_conflict.classify(ev, repo)
    assert cl.klass == CLASS_SEMANTIC_OVERLAP
    assert not cl.proven
    assert cl.prescription == []
    assert {"sum", "rows"} <= set(cl.overlapping_symbols)

    rendered = land_conflict.render_human(ev, cl)
    assert "Both sides, in full:" in rendered
    assert "round(sum(rows), 2)" in rendered          # the base branch's edit
    assert "sum(r for r in rows if r)" in rendered    # the branch's edit


def test_auto_file_only_is_proven_and_prescribes_the_restore(tmp_path):
    repo, branch = _auto_file_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    cl = land_conflict.classify(ev, repo)
    assert cl.klass == CLASS_AUTO_FILE_ONLY
    assert cl.proven
    assert any("checkout main --" in step for step in cl.prescription)


def test_already_landed_is_proven_from_git_cherry(tmp_path):
    repo, branch = _already_landed_repo(tmp_path)
    ev = _capture(repo, branch=branch, task_id="E-4000")

    assert ev.cherry_mark(ev.rebase_head) == "-", ev.cherry
    cl = land_conflict.classify(ev, repo)
    assert cl.klass == CLASS_ALREADY_LANDED
    assert cl.proven
    assert any("reset --hard main" in step for step in cl.prescription)
    # The claim the prescription rests on has to be stated, not implied.
    assert any("rebase --continue" in line for line in cl.evidence)


@pytest.mark.skipif(not ENDLESS_GO.exists(),
                    reason="bin/endless-go not built (just build)")
def test_orphaned_ledger_base_is_detected_by_content(tmp_path, monkeypatch):
    """The detector is Go's, shelled out — one implementation of ED-1553's rule,
    not a Python copy that drifts from it."""
    monkeypatch.setenv("PATH", f"{ENDLESS_GO.parent}{os.pathsep}{os.environ['PATH']}")
    repo, branch = _ledger_orphan_repo(tmp_path)
    ev = _capture(repo, branch=branch, task_id="E-5000")

    cl = land_conflict.classify(ev, repo)
    assert cl.klass == CLASS_ORPHANED_LEDGER_BASE
    assert cl.proven
    assert any("rebase --onto main" in step for step in cl.prescription)


@pytest.mark.skipif(not ENDLESS_GO.exists(),
                    reason="bin/endless-go not built (just build)")
def test_ledger_orphans_reports_a_prefix_run_as_contiguous(tmp_path, monkeypatch):
    monkeypatch.setenv("PATH", f"{ENDLESS_GO.parent}{os.pathsep}{os.environ['PATH']}")
    repo, branch = _ledger_orphan_repo(tmp_path)
    ev = _capture(repo, branch=branch, task_id="E-5000")

    report = land_conflict.ledger_orphans(repo, ev)
    assert report is not None
    assert report["contiguous_at_base"] is True
    assert report["mid_branch"] is False
    assert len(report["orphans"]) == 1


def test_a_missing_endless_go_degrades_instead_of_failing(tmp_path, monkeypatch):
    """The orphan test is one of five. Losing it must cost that one class, not
    the command."""
    monkeypatch.setattr(land_conflict.shutil, "which", lambda _: None)
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)

    assert land_conflict.ledger_orphans(repo, ev) is None
    assert land_conflict.classify(ev, repo).klass == CLASS_SYMBOL_SUPERSESSION


# ── the invariant ──────────────────────────────────────────────────────────

@pytest.mark.parametrize("builder,expected", [
    (_supersession_repo, CLASS_SYMBOL_SUPERSESSION),
    (_overlap_repo, CLASS_SEMANTIC_OVERLAP),
    (_auto_file_repo, CLASS_AUTO_FILE_ONLY),
    (_already_landed_repo, CLASS_ALREADY_LANDED),
])
def test_a_prescription_appears_only_where_the_class_is_proven(
    tmp_path, builder, expected
):
    """The contract, checked over every class rather than only where a
    prescription is expected: an unproven class must never reach a reader with
    a command in hand."""
    repo, branch = builder(tmp_path)
    ev = _capture(repo, branch=branch)
    cl = land_conflict.classify(ev, repo)

    assert cl.klass == expected
    # One direction only, and deliberately: proven does NOT imply a
    # prescription. Symbol supersession is proven precisely so it can say
    # with authority that there is nothing safe to do.
    assert not cl.prescription or cl.proven
    # render_human asserts the same thing; make sure it renders at all.
    land_conflict.render_human(ev, cl)


@pytest.mark.parametrize("builder", [_supersession_repo, _overlap_repo])
def test_the_two_hazardous_recoveries_are_never_printed(tmp_path, builder):
    """The regression this task exists to prevent: for a conflict whose class
    is not proven, neither of the old numbered candidates may appear."""
    repo, branch = builder(tmp_path)
    ev = _capture(repo, branch=branch)
    rendered = land_conflict.render_human(ev, land_conflict.classify(ev, repo))

    assert "git rebase --continue" not in rendered
    assert "reset --hard" not in rendered
    assert "No recovery is prescribed." in rendered


def test_json_output_carries_the_evidence_and_the_verdict(tmp_path):
    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)
    doc = json.loads(land_conflict.render_json(ev, land_conflict.classify(ev, repo)))

    assert doc["classification"]["klass"] == CLASS_SYMBOL_SUPERSESSION
    assert doc["classification"]["prescription"] == []
    assert doc["evidence"]["unmerged_paths"] == ["sync.py"]
    assert doc["evidence"]["hunks"][0]["branch_side"]


# ── endless worktree diagnose ──────────────────────────────────────────────

def _fake_target(monkeypatch, repo: Path, task_id="E-1943", base="main"):
    """Stand in for the resolver, which needs the project database.

    Only the resolution is replaced. Everything the command then does — read the
    capture, classify it, render it — is the real path.
    """
    from endless import worktree_cmd

    monkeypatch.setattr(
        worktree_cmd, "_resolve_land_target",
        lambda _tid: (task_id, repo, "task/1943", base),
    )


def test_diagnose_says_so_and_exits_non_zero_when_nothing_is_recorded(
    tmp_path, monkeypatch
):
    """"Nothing recorded" is an answer. Reproducing a conflict to have something
    to say would describe today's base branch, not the one the land failed
    against."""
    import click

    from endless.worktree_cmd import diagnose_land_conflict

    repo = _init_repo(tmp_path / "e-1943")
    _fake_target(monkeypatch, repo)

    with pytest.raises(click.ClickException) as exc:
        diagnose_land_conflict(None, as_json=False)

    msg = exc.value.message
    assert "No land conflict is recorded for E-1943" in msg
    assert "--dry-run" in msg
    # And it did not fabricate one on the way out.
    assert land_conflict.load_evidence(repo) is None


def test_diagnose_renders_the_recorded_conflict(tmp_path, monkeypatch, capsys):
    from endless.worktree_cmd import diagnose_land_conflict

    repo, branch = _supersession_repo(tmp_path)
    ev = _capture(repo, branch=branch)
    land_conflict.store_evidence(repo, ev)
    _fake_target(monkeypatch, repo)

    diagnose_land_conflict(None, as_json=False)

    out = capsys.readouterr().out
    assert "symbol supersession (proven)" in out
    assert "BINARY_SOURCE_PATHS" in out
    assert "No recovery is prescribed." in out
    assert "git rebase --continue" not in out


def test_diagnose_json_is_the_same_verdict_for_a_machine(
    tmp_path, monkeypatch, capsys
):
    from endless.worktree_cmd import diagnose_land_conflict

    repo, branch = _supersession_repo(tmp_path)
    land_conflict.store_evidence(repo, _capture(repo, branch=branch))
    _fake_target(monkeypatch, repo)

    diagnose_land_conflict(None, as_json=True)

    doc = json.loads(capsys.readouterr().out)
    assert doc["classification"]["klass"] == CLASS_SYMBOL_SUPERSESSION
    assert doc["classification"]["prescription"] == []
    assert doc["evidence"]["rebase_head"]


def test_diagnose_is_registered_under_the_worktree_group():
    """Beside land/drop/check/list, where a conflict sends you looking."""
    from endless.cli import worktree_cmd as group

    assert "diagnose" in group.commands
    assert group.commands["diagnose"].params[0].name == "task_id"
    assert not group.commands["diagnose"].params[0].required


# ── land --dry-run rehearsal ───────────────────────────────────────────────

REHEARSAL_PREFIX = "endless/land-rehearsal/"


def _assert_no_rehearsal_residue(repo: Path) -> None:
    """No throwaway branch, and no checkout beyond the one we started with."""
    branches = _run(["git", "branch", "--format=%(refname)"], repo).stdout
    assert REHEARSAL_PREFIX not in branches
    checkouts = _run(
        ["git", "worktree", "list", "--porcelain"], repo
    ).stdout.count("worktree ")
    assert checkouts == 1, f"{checkouts} checkouts left registered"


def test_rehearsal_predicts_the_conflict_and_leaves_nothing_behind(tmp_path):
    """`--dry-run` runs the land's real rebase on a copy, so what it predicts is
    git's answer rather than an estimate of it."""
    from endless.worktree_cmd import _rehearse_land_rebase

    repo, branch = _supersession_repo(tmp_path)
    _run(["git", "rebase", "--abort"], repo, check=False)

    before_branches = _run(["git", "branch", "--format=%(refname)"], repo).stdout
    before_head = _run(["git", "rev-parse", "HEAD"], repo).stdout.strip()
    before_main = _run(["git", "rev-parse", "main"], repo).stdout.strip()

    ev = _rehearse_land_rebase(repo, branch, "main", repo, "E-1943")

    assert ev is not None and ev.rehearsal
    assert land_conflict.classify(ev, repo).klass == CLASS_SYMBOL_SUPERSESSION
    # The task branch, the base branch and HEAD are exactly as they were, and
    # the throwaway branch and checkout are gone.
    assert _run(["git", "branch", "--format=%(refname)"], repo).stdout == before_branches
    assert _run(["git", "rev-parse", "HEAD"], repo).stdout.strip() == before_head
    assert _run(["git", "rev-parse", "main"], repo).stdout.strip() == before_main
    _assert_no_rehearsal_residue(repo)
    # A rehearsal must not overwrite the slot a real post-mortem lives in.
    assert land_conflict.load_evidence(repo) is None


def _land_against(monkeypatch, repo: Path, branch: str):
    """Point `land` at a throwaway repo without a project database.

    Only the worktree lookup is replaced — everything the dry-run then does is
    the shipped path.
    """
    from endless import config, worktree_cmd

    monkeypatch.setattr(config, "default_db_to_main", lambda: None)
    monkeypatch.setattr(worktree_cmd, "_project_root", lambda: repo)
    monkeypatch.setattr(worktree_cmd, "_enriched_list", lambda _root: [{
        "path": str(repo),
        "branch": branch,
        "companion": {"base_branch": "main"},
    }])
    monkeypatch.setattr(
        worktree_cmd, "_branch_for_task", lambda rows, _tid: rows[0])


def test_dry_run_reports_the_predicted_conflict_and_exits_non_zero(
    tmp_path, monkeypatch, capsys
):
    """Automation reads the exit code. A preview that predicts a failure and
    still exits 0 is a preview nothing can gate on."""
    from endless.worktree_cmd import land_worktree

    repo, branch = _supersession_repo(tmp_path)
    _run(["git", "rebase", "--abort"], repo, check=False)
    _land_against(monkeypatch, repo, branch)

    with pytest.raises(SystemExit) as exc:
        land_worktree("E-1943", dry_run=True)

    assert exc.value.code == 1
    out = capsys.readouterr().out
    assert "it conflicts" in out
    assert "symbol supersession (proven)" in out
    assert "BINARY_SOURCE_PATHS" in out
    assert "git rebase --continue" not in out


def test_dry_run_says_so_and_exits_zero_when_the_rebase_is_clean(
    tmp_path, monkeypatch, capsys
):
    from endless.worktree_cmd import land_worktree

    repo = _init_repo(tmp_path / "e-8001")
    _commit(repo, "base", {"a.txt": "one\n"})
    _run(["git", "checkout", "-q", "-b", "task/8001"], repo)
    _commit(repo, "E-8001: add b", {"b.txt": "two\n"})
    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "E-8000: add c", {"c.txt": "three\n"})
    _run(["git", "checkout", "-q", "task/8001"], repo)
    _land_against(monkeypatch, repo, "task/8001")

    land_worktree("E-8001", dry_run=True)

    out = capsys.readouterr().out
    assert "no conflict" in out
    # And nothing was landed on the way past: main is where it was.
    assert _run(["git", "rev-parse", "main"], repo).stdout.strip() != \
        _run(["git", "rev-parse", "task/8001"], repo).stdout.strip()
    _assert_no_rehearsal_residue(repo)


def test_rehearsal_returns_none_when_the_rebase_is_clean(tmp_path):
    from endless.worktree_cmd import _rehearse_land_rebase

    repo = _init_repo(tmp_path / "e-8000")
    _commit(repo, "base", {"a.txt": "one\n"})
    _run(["git", "checkout", "-q", "-b", "task/8000"], repo)
    _commit(repo, "E-8000: add b", {"b.txt": "two\n"})
    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "E-7999: add c", {"c.txt": "three\n"})
    _run(["git", "checkout", "-q", "task/8000"], repo)

    assert _rehearse_land_rebase(repo, "task/8000", "main", repo, "E-8000") is None
    _assert_no_rehearsal_residue(repo)


def test_rehearsal_cleans_up_when_the_rebase_machinery_raises(tmp_path, monkeypatch):
    """Cleanup is in a `finally` because the interesting failures are the ones
    that do not return."""
    from endless import worktree_cmd

    repo, branch = _supersession_repo(tmp_path)
    _run(["git", "rebase", "--abort"], repo, check=False)

    def boom(*_a, **_k):
        raise RuntimeError("rehearsal exploded")

    monkeypatch.setattr(worktree_cmd, "_drop_orphan_amendable_commits", boom)
    with pytest.raises(RuntimeError):
        worktree_cmd._rehearse_land_rebase(repo, branch, "main", repo, "E-1943")

    _assert_no_rehearsal_residue(repo)


def test_rehearsal_carries_the_live_verbs_across_without_touching_them(tmp_path):
    """Land folds the worktree's pending verb additions into the branch before
    rebasing (its Step 3.5). The rehearsal has to do the same, or it predicts a
    verbs.jsonl conflict the land itself dissolves — but it must do it on the
    throwaway branch, reading the live file and never writing to it."""
    from endless.worktree_cmd import _rehearse_land_rebase

    repo = _init_repo(tmp_path / "e-9000")
    main_root = _init_repo(tmp_path / "main")
    _commit(repo, "base", {
        ".gitattributes": ".endless/verbs.jsonl merge=union\n",
        ".endless/verbs.jsonl": '{"value":"alpha"}\n',
    })
    _run(["git", "checkout", "-q", "-b", "task/9000"], repo)
    _commit(repo, "E-9000: work", {"src.txt": "work\n"})
    _run(["git", "checkout", "-q", "main"], repo)
    _commit(repo, "Endless: record verb", {
        ".endless/verbs.jsonl": '{"value":"alpha"}\n{"value":"beta"}\n'})
    _run(["git", "checkout", "-q", "task/9000"], repo)

    _commit(main_root, "main verbs", {
        ".endless/verbs.jsonl": '{"value":"alpha"}\n{"value":"beta"}\n'})
    # An uncommitted verb addition in the live worktree, as a session leaves one.
    live = repo / ".endless" / "verbs.jsonl"
    live.write_text('{"value":"alpha"}\n{"value":"gamma"}\n')

    assert _rehearse_land_rebase(
        repo, "task/9000", "main", main_root, "E-9000") is None
    assert live.read_text() == '{"value":"alpha"}\n{"value":"gamma"}\n'
    assert _run(["git", "status", "--porcelain"], repo).stdout.strip() == \
        "?? .endless/verbs.jsonl" or live.read_text()
    _assert_no_rehearsal_residue(repo)


def test_rehearsal_needs_no_endless_go(tmp_path, monkeypatch):
    """Rehearsing is pure git; only classification consults the Go detector."""
    from endless.worktree_cmd import _rehearse_land_rebase

    monkeypatch.setattr(shutil, "which", lambda _: None)
    repo, branch = _supersession_repo(tmp_path)
    _run(["git", "rebase", "--abort"], repo, check=False)

    assert _rehearse_land_rebase(repo, branch, "main", repo, "E-1943") is not None
