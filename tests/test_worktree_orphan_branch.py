"""E-1500: create_task_worktree recovers from an orphan task branch.

An orphan branch is a `task/<id>-<slug>` branch whose worktree directory is
gone — left behind by `worktree drop` (git worktree remove keeps the branch)
or by the land/reap path. Before E-1500, the next claim/spawn hit
`git worktree add -b <branch>` -> "a branch already exists" with no
remediation. Now create_task_worktree classifies the orphan's delta from main
and either recreates fresh (plan-only / no work) or refuses with an actionable
message (real work).
"""

import subprocess
from pathlib import Path

import click
import pytest

from endless import db, worktree_cmd
from endless.worktree_cmd import create_task_worktree, _plan_viable, _slugify_title


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


# A plan body comfortably over PLAN_VIABILITY_MIN_CHARS (128).
VIABLE = "x" * 200


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
    branch = f"task/{tid}-{_slugify_title(title)}"
    return {"root": repo, "tid": tid, "title": title, "branch": branch}


def _make_orphan_branch(repo: Path, branch: str, files: dict[str, str] | None, msg: str):
    """Create `branch` at main, optionally commit `files` (repo-relative path ->
    content) on it via a throwaway worktree, then remove that worktree — leaving
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
        _run(["git", "worktree", "remove", "--force", str(scratch)], repo)


def _branches(repo: Path) -> str:
    return subprocess.run(
        ["git", "branch"], cwd=str(repo), capture_output=True, text=True, check=True,
    ).stdout


def _worktree_dir(p) -> Path:
    return p["root"] / ".endless" / "worktrees" / f"e-{p['tid']}"


# --- safe paths: recreate fresh --------------------------------------------

def test_plan_only_orphan_text_matches_recreates_fresh(project_with_task, monkeypatch):
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: VIABLE}, "plan")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: VIABLE)

    wt_path, created = create_task_worktree(p["tid"], p["title"], p["root"])

    assert created is True
    assert wt_path.exists()
    assert p["branch"] in _branches(p["root"])


def test_empty_delta_orphan_recreates_fresh(project_with_task, monkeypatch):
    """Branch is an ancestor of main (no unique commits) -> trivially safe."""
    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], None, "")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: VIABLE)

    wt_path, created = create_task_worktree(p["tid"], p["title"], p["root"])

    assert created is True
    assert wt_path.exists()


def test_plan_only_orphan_db_empty_adopts_file(project_with_task, monkeypatch):
    """tasks.text empty + viable committed plan -> recover the file into the DB."""
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    file_text = "# Recovered plan\n\n" + "z" * 200
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: file_text}, "plan")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: "")

    wt_path, created = create_task_worktree(p["tid"], p["title"], p["root"])

    assert created is True
    assert wt_path.exists()
    row = db.query("SELECT text FROM tasks WHERE id = ?", (p["tid"],))[0]
    assert row["text"].strip() == file_text.strip()


# --- refusal paths: actionable errors, branch preserved ---------------------

def test_plan_only_orphan_mismatch_raises(project_with_task, monkeypatch):
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: "FILE " + "a" * 200}, "plan")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: "DB " + "b" * 200)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["title"], p["root"])

    msg = str(exc.value)
    assert "differs" in msg
    assert "chars)" in msg  # char counts shown, not full dump
    assert f"endless task show E-{p['tid']} --text" in msg
    assert f"branch -D {p['branch']}" in msg
    assert f"endless task update E-{p['tid']} --text" in msg
    # branch preserved (error before any delete); no worktree created
    assert p["branch"] in _branches(p["root"])
    assert not _worktree_dir(p).exists()


def test_plan_only_orphan_db_text_not_viable_raises(project_with_task, monkeypatch):
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: VIABLE}, "plan")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: "one short line")

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["title"], p["root"])

    msg = str(exc.value)
    assert "too short to be a viable plan" in msg
    assert f"endless task update E-{p['tid']} --text" in msg
    assert p["branch"] in _branches(p["root"])


def test_plan_only_orphan_db_empty_file_not_viable_raises(project_with_task, monkeypatch):
    p = project_with_task
    plan_rel = f".endless/plans/E-{p['tid']}.md"
    _make_orphan_branch(p["root"], p["branch"], {plan_rel: "too short"}, "plan")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: "")

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["title"], p["root"])

    assert "no viable plan" in str(exc.value).lower()
    assert p["branch"] in _branches(p["root"])


def test_real_work_orphan_raises(project_with_task, monkeypatch):
    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], {"src/foo.py": "print('x')\n"}, "code")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: VIABLE)

    with pytest.raises(click.ClickException) as exc:
        create_task_worktree(p["tid"], p["title"], p["root"])

    msg = str(exc.value)
    assert "non-plan files" in msg
    assert "src/foo.py" in msg
    assert f"git -C" in msg and f"log main..{p['branch']}" in msg
    assert f"branch -D {p['branch']}" in msg
    assert p["branch"] in _branches(p["root"])  # preserved, not auto-discarded


def test_status_untouched_when_orphan_refuses(project_with_task, monkeypatch):
    """E-1500: securing the worktree before the status flip means a refusal
    leaves the task's status unchanged (no stranded in_progress)."""
    from endless.task_cmd import claim_item

    p = project_with_task
    _make_orphan_branch(p["root"], p["branch"], {"src/foo.py": "print('x')\n"}, "code")
    monkeypatch.setattr(worktree_cmd, "_read_task_text", lambda *a, **k: VIABLE)

    with pytest.raises(click.ClickException):
        claim_item(p["tid"], force=True)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (p["tid"],))[0]
    assert row["status"] == "ready"


# --- threshold unit ---------------------------------------------------------

def test_plan_viable_threshold():
    assert _plan_viable("x" * 127) is False
    assert _plan_viable("x" * 128) is True
    assert _plan_viable("  " + "x" * 128 + "  ") is True  # stripped before measuring
    assert _plan_viable("") is False
