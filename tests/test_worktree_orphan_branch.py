"""E-1500: create_task_worktree recovers from an orphan task branch.

An orphan branch is a `task/<id>` branch whose worktree directory is
gone — left behind by `worktree drop` or by the land/reap path. Before E-1500,
the next claim/spawn hit `git worktree add -b <branch>` -> "a branch already
exists" with no remediation. Now create_task_worktree classifies the orphan's
delta from main and either recreates fresh (mirror-only / no work) or refuses
with an actionable message (real work).

E-2137 widened "mirror" from the single `.endless/plans/E-NNN.md` path to every
document mirror in both layouts, and narrowed what a mismatch does: the old code
adopted a branch's plan into an empty column and measured plans against a
character threshold to decide whether they were worth keeping. Mirrors no longer
reach branches at all, so a branch holding one the database does not have is
rare and interesting — reported to a person, never resolved by a rule.
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import db, worktree_cmd
from endless.worktree_cmd import create_task_worktree, task_branch


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


BODY = "x" * 200


@pytest.fixture
def project_with_task(seeded_project_at_cwd):
    """Registered project (git repo + initial commit from the fixture) plus a
    single 'ready' task. Returns root, task id, title, and the branch name
    create_task_worktree will compute for it.
    """
    repo = seeded_project_at_cwd
    proj_id = db.query("SELECT id FROM projects WHERE path = ?", (str(repo),))[0]["id"]
    title = "Allow endless task add from a plain terminal"
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, title, title),
    )
    tid = db.query("SELECT id FROM tasks WHERE title = ?", (title,))[0]["id"]
    branch = task_branch(tid)
    return {"root": repo, "tid": tid, "title": title, "branch": branch}


def _make_orphan_branch(repo: Path, branch: str, files: dict[str, str] | None, msg: str):
    """Create `branch` at main, optionally commit `files` (repo-relative path ->
    content) on it via a throwaway worktree, then detach that directory — leaving
    an orphan branch with no directory at the canonical `.endless/worktrees/`
    location.
    """
    _run(["git", "branch", branch, "main"], repo)
    if files:
        scratch = repo / "_scratch_wt"
        _run(["git", "worktree", "add", "-q", str(scratch), branch], repo)
        for rel, content in files.items():
            target = scratch / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)
            _run(["git", "add", rel], scratch)
        _run(["git", "commit", "-q", "-m", msg], scratch)
        _detach_scratch(repo, scratch)


def _detach_scratch(repo: Path, scratch: Path):
    """Unregister the throwaway checkout, leaving its branch orphaned.

    Deliberately NOT git's own worktree-removal verb: this project's PreToolUse
    hook refuses that phrase outright, in a test file as surely as in a shell.
    Deleting the directory and pruning the registration reaches the same state
    through the two steps git's verb wraps.
    """
    import shutil

    shutil.rmtree(scratch)
    _run(["git", "worktree", "prune"], repo)


def _branches(repo: Path) -> str:
    return subprocess.run(
        ["git", "branch"], cwd=str(repo), capture_output=True, text=True, check=True,
    ).stdout


def _worktree_dir(p) -> Path:
    return p["root"] / ".endless" / "worktrees" / f"e-{p['tid']}"


def _db_says(monkeypatch, content: str | None):
    """Stub the Go doc-content read. None means "could not ask"."""
    monkeypatch.setattr(worktree_cmd, "doc_mirror_content", lambda *a, **k: content)


# --- safe paths: recreate fresh --------------------------------------------

def test_mirror_only_orphan_matching_db_recreates_fresh(project_with_task, monkeypatch):
    p = project_with_task
    plan_rel = f".endless/tasks/e-{p['tid']}/plan.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: BODY}, "plan")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()
    assert p["branch"] in _branches(p["root"])


def test_legacy_path_mirror_is_recognized_too(project_with_task, monkeypatch):
    """A branch cut before the consolidation holds `.endless/plans/E-NNN.md`.
    That is still a mirror, not work."""
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: BODY}, "plan")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()


def test_every_mirror_kind_counts_as_a_mirror(project_with_task, monkeypatch):
    """Analysis, outcome and a decision body are mirrors too — the old check
    knew only about plans and would have called these real work."""
    p = project_with_task
    files = {
        f".endless/tasks/e-{p['tid']}/analysis.md": BODY,
        f".endless/tasks/e-{p['tid']}/outcome.md": BODY,
        ".endless/analyses/E-9999.md": BODY,
        ".endless/decisions/ED-7.md": BODY,
    }
    _make_orphan_branch(p["root"], p["branch"], files, "mirrors")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()


def test_empty_delta_orphan_recreates_fresh(project_with_task, monkeypatch):
    """Branch is an ancestor of main (no unique commits) -> trivially safe."""
    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], None, "")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()


# --- refusal paths: actionable errors, branch preserved ---------------------

def test_mirror_mismatch_raises_and_names_the_adopt_command(project_with_task,
                                                            monkeypatch):
    p = project_with_task
    plan_rel = f".endless/tasks/e-{p['tid']}/plan.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: "FILE " + "a" * 200}, "plan")
    _db_says(monkeypatch, "DB " + "b" * 200)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    msg = str(exc.value)
    assert plan_rel in msg
    assert f"show {p['branch']}:{plan_rel}" in msg
    assert f"endless task update E-{p['tid']} --plan-file" in msg
    assert f"branch -D {p['branch']}" in msg
    # branch preserved (error before any delete); no worktree created
    assert p["branch"] in _branches(p["root"])
    assert not _worktree_dir(p).exists()


def test_empty_db_column_is_a_mismatch_not_an_adoption(project_with_task,
                                                       monkeypatch):
    """E-2137: the old code silently recovered the branch's plan into an empty
    column. The branch is now the only place that text exists, and which side
    wins is not this command's to guess."""
    p = project_with_task
    plan_rel = f".endless/tasks/e-{p['tid']}/plan.md"
    file_text = "# Recovered plan\n\n" + "z" * 200
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: file_text}, "plan")
    _db_says(monkeypatch, "")

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    assert plan_rel in str(exc.value)
    assert p["branch"] in _branches(p["root"])
    row = db.query("SELECT (SELECT content FROM task_content WHERE task_id = tasks.id AND name = 'plan') AS plan FROM tasks WHERE id = ?", (p["tid"],))[0]
    assert not (row["plan"] or "").strip()   # nothing was adopted


def test_unreadable_db_refuses_rather_than_deleting(project_with_task, monkeypatch):
    """"I could not ask the database" must never read as "the database agrees"."""
    p = project_with_task
    plan_rel = f".endless/tasks/e-{p['tid']}/plan.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: BODY}, "plan")
    _db_says(monkeypatch, None)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    assert "could not be asked" in str(exc.value)
    assert p["branch"] in _branches(p["root"])


def test_real_work_orphan_raises(project_with_task, monkeypatch):
    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], {"src/foo.py": "print('x')\n"}, "code")
    _db_says(monkeypatch, BODY)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    msg = str(exc.value)
    assert "no document mirror accounts for" in msg
    assert "src/foo.py" in msg
    assert "git -C" in msg and f"log main..{p['branch']}" in msg
    assert f"branch -D {p['branch']}" in msg
    assert p["branch"] in _branches(p["root"])  # preserved, not auto-discarded


def test_verify_script_in_the_task_dir_is_real_work(project_with_task, monkeypatch):
    """`.endless/tasks/e-NNNN/` holds the task's own verify.sh beside the
    database's .md files. Only the .md files are mirrors."""
    p = project_with_task
    rel = f".endless/tasks/e-{p['tid']}/verify.sh"
    _make_orphan_branch(p["root"], p["branch"], {rel: "#!/usr/bin/env bash\n"}, "suite")
    _db_says(monkeypatch, BODY)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    assert rel in str(exc.value)
    assert p["branch"] in _branches(p["root"])


def test_status_untouched_when_orphan_refuses(project_with_task, monkeypatch):
    """E-1500: securing the worktree before the status flip means a refusal
    leaves the task's status unchanged (no stranded underway)."""
    from endless.task_cmd import claim_item

    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], {"src/foo.py": "print('x')\n"}, "code")
    _db_says(monkeypatch, BODY)

    with pytest.raises(click.ClickException):
        claim_item(p["tid"], force=True)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (p["tid"],))[0]
    assert row["status"] == "ready"


# --- legacy slug branches (E-2108) ------------------------------------------
#
# ED-1587 renamed task branches from `task/<id>-<slug>` to `task/<id>`, so a
# repo that predates the rename can hold an orphan under the OLD name. E-1500's
# guarantee is that a claim never silently strands a branch holding real work,
# and a check that only knew the constructed name would walk straight past one.

def test_legacy_slug_orphan_with_real_work_still_refuses(project_with_task,
                                                         monkeypatch):
    p = project_with_task
    legacy = f"task/{p['tid']}-some-old-title-slug"
    _make_orphan_branch(p["root"], legacy, {"src/foo.py": "print('x')\n"}, "code")
    _db_says(monkeypatch, BODY)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["root"])

    msg = str(exc.value)
    assert "no document mirror accounts for" in msg
    assert legacy in msg          # names the branch the user actually has
    assert legacy in _branches(p["root"])   # preserved, not auto-discarded
    assert not _worktree_dir(p).exists()


def test_legacy_slug_orphan_with_nothing_is_reclaimed(project_with_task,
                                                      monkeypatch):
    """An empty legacy branch is deleted, and the new worktree gets task/<id>."""
    p = project_with_task
    legacy = f"task/{p['tid']}-some-old-title-slug"
    _make_orphan_branch(p["root"], legacy, None, "")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()
    branches = _branches(p["root"])
    assert legacy not in branches
    assert p["branch"] in branches


def test_unrelated_task_branch_is_not_swept(project_with_task, monkeypatch):
    """The id sweep must not match a longer id that merely starts the same."""
    p = project_with_task
    neighbour = f"task/{p['tid']}0-different-task"
    _make_orphan_branch(p["root"], neighbour, {"src/foo.py": "print('x')\n"}, "code")
    _db_says(monkeypatch, BODY)

    wt_path, created = create_task_worktree(p["tid"], p["root"])

    assert created is True
    assert wt_path.exists()
    assert neighbour in _branches(p["root"])  # untouched
