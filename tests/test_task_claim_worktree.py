"""Tests for E-971 Layer F: end-to-end worktree creation on `task claim`.

Exercises claim_item against a real git project + DB.
"""

import json
import subprocess
from pathlib import Path

import click
import pytest

from endless import db


def _run(cmd, cwd):
    subprocess.run(cmd, cwd=str(cwd), check=True, capture_output=True)


@pytest.fixture
def project_with_task(seeded_project_at_cwd):
    """tmp dir with: git repo, registered project at cwd, one task inserted.

    Returns dict with: project_root, task_id, title.
    """
    repo = seeded_project_at_cwd
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)

    proj_id = db.query("SELECT id FROM projects WHERE path = ?", (str(repo),))[0]["id"]
    title = "Move title verbs from hardcoded list to database table"
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, title, title),
    )
    task_id = db.query("SELECT id FROM tasks WHERE title = ?", (title,))[0]["id"]
    return {"project_root": repo, "task_id": task_id, "title": title}


def test_claim_refuses_settled_status_and_names_the_reopen_route(project_with_task):
    """E-1235 refused; E-2093 changed what the refusal offers.

    No flag clears this gate any more. The refusal has to name a command that
    works FROM THE STATUS THAT PRODUCED IT, and that is two different commands:
    shipped work reopens to `revisit`, while `declined`/`obsolete` never
    shipped and the lifecycle reverses those to `untriaged` instead — it has no
    edge from either to `revisit` at all.
    """
    from endless.task_cmd import claim_item

    tid = project_with_task["task_id"]
    routes = {
        "unverified": "revisit",
        "unreviewed": "revisit",
        "confirmed": "revisit",
        "assumed": "revisit",
        "completed": "revisit",
        "declined": "untriaged",
        "obsolete": "untriaged",
    }
    for status, target in routes.items():
        db.execute("UPDATE tasks SET status = ? WHERE id = ?", (status, tid))
        with pytest.raises(click.ClickException) as exc:
            claim_item(tid)
        msg = str(exc.value)
        assert f"E-{tid} is in status '{status}'" in msg
        assert f"endless task update E-{tid} --status {target}" in msg
        assert f"endless task claim E-{tid}" in msg
        # The bypass is gone; the refusal must not resurrect it.
        assert "--force" not in msg
        # Status must not have changed
        row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]
        assert row["status"] == status


def test_claim_reopen_route_then_claim_actually_works(project_with_task):
    """E-2093: the route the refusal names must BE a route, not a suggestion.

    This is the entire replacement for the removed demotion bypass, so it is
    walked end to end for both halves of the split.
    """
    from endless.task_cmd import claim_item, update_plan

    tid = project_with_task["task_id"]
    for settled, target in (("confirmed", "revisit"), ("declined", "untriaged")):
        db.execute("UPDATE tasks SET status = ? WHERE id = ?", (settled, tid))
        update_plan(tid, status=target)
        claim_item(tid, unattended=True)
        row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]
        assert row["status"] == "underway", (
            f"reopening {settled} -> {target} then claiming did not reach underway"
        )


def test_claim_force_still_demotes_but_warns(project_with_task, capsys):
    """E-2093: `--force` works for one release and announces its own removal."""
    from endless.task_cmd import claim_item

    tid = project_with_task["task_id"]
    db.execute("UPDATE tasks SET status = 'unverified' WHERE id = ?", (tid,))

    claim_item(tid, force=True)

    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]
    assert row["status"] == "underway"

    err = capsys.readouterr().err
    assert "`endless task claim --force` is deprecated" in err
    # Both halves are named, so the warning is actionable for either caller.
    assert "--unattended" in err
    assert f"endless task update E-{tid} --status revisit" in err


def test_claim_refuses_when_no_session_and_names_unattended(project_with_task):
    """E-1242 refused; E-2093 renamed the flag that clears it.

    `--force`'s undocumented second half is now `--unattended`, and this
    refusal is the only place a caller learns the flag exists — so it names the
    new one and not the deprecated one.
    """
    from unittest.mock import patch
    from endless.task_cmd import claim_item, _reset_session_choice_cache

    _reset_session_choice_cache()
    tid = project_with_task["task_id"]
    with patch(
        "endless.task_cmd._resolve_session_id_with_prompt",
        return_value=None,
    ):
        with pytest.raises(click.ClickException) as exc:
            claim_item(tid)
    msg = str(exc.value)
    assert "No Claude session available" in msg
    assert "--unattended" in msg
    assert "--force" not in msg


def test_claim_unattended_is_only_the_session_half(project_with_task):
    """E-2093: `--unattended` does exactly one thing — claim with no session.

    The point of splitting `--force` is that each half now decides one thing,
    so `--unattended` must NOT also clear the settled-status gate.
    """
    from unittest.mock import patch
    from endless.task_cmd import claim_item, _reset_session_choice_cache

    _reset_session_choice_cache()
    tid = project_with_task["task_id"]
    with patch(
        "endless.task_cmd._resolve_session_id_with_prompt",
        return_value=None,
    ):
        claim_item(tid, unattended=True)
        assert db.query(
            "SELECT status FROM tasks WHERE id = ?", (tid,)
        )[0]["status"] == "underway"

        db.execute("UPDATE tasks SET status = 'confirmed' WHERE id = ?", (tid,))
        with pytest.raises(click.ClickException) as exc:
            claim_item(tid, unattended=True)
    assert "re-claiming would demote it" in " ".join(str(exc.value).split())


def test_claim_binds_sibling_claude_session(project_with_task):
    """E-1242: a resolved (or user-picked) sibling Claude session is used."""
    from unittest.mock import patch
    from endless.task_cmd import claim_item, _reset_session_choice_cache

    _reset_session_choice_cache()
    tid = project_with_task["task_id"]
    SIBLING_EID = 4242

    with patch(
        "endless.task_cmd._resolve_session_id_with_prompt",
        return_value=SIBLING_EID,
    ):
        claim_item(tid)

    # task status flipped (event-sourced through Go executor)
    row = db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]
    assert row["status"] == "underway"


def test_claim_creates_worktree_no_plan_file(project_with_task, capsys):
    from endless.task_cmd import claim_item

    claim_item(project_with_task["task_id"], unattended=True)

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert wt.exists()
    assert wt.is_dir()

    companion = json.loads((wt / ".endless" / "worktree.json").read_text())
    assert companion["kind"] == "task"
    # E-1301: task_id is no longer written; the worktree's identity comes
    # from the path convention `.endless/worktrees/e-NNN`. The companion's
    # presence is the "endless-managed" marker; its content documents the
    # worktree's provenance (base_branch, branch, created_at).
    assert "task_id" not in companion
    assert companion["base_branch"] == "main"
    assert companion["branch"].startswith(f"task/{tid}-")
    assert "created_at" in companion

    # Slug should derive from title
    assert companion["branch"] == f"task/{tid}-move-title-verbs-hardcoded-list-database"

    # Branch exists in git
    branches = subprocess.run(
        ["git", "branch"], cwd=repo, capture_output=True, text=True, check=True,
    ).stdout
    assert f"task/{tid}-move-title-verbs-hardcoded-list-database" in branches

    # User-facing output: new format with "worktree created", spawn option,
    # and the eswt helper command. Defaults to verbose form because no
    # SHELL is set in the test environment (or eswt isn't defined there).
    captured = capsys.readouterr()
    assert "worktree created:" in captured.out
    assert f"endless task spawn E-{tid}" in captured.out
    assert f"eswt E-{tid}" in captured.out
    assert 'eval "$(endless shell-init)"' in captured.out


def test_claim_idempotent_on_second_run(project_with_task, capsys):
    from endless.task_cmd import claim_item

    claim_item(project_with_task["task_id"], unattended=True)
    capsys.readouterr()  # clear

    claim_item(project_with_task["task_id"], unattended=True)
    captured = capsys.readouterr()
    assert "worktree already exists:" in captured.out
    # Re-run still shows the same two-option block
    tid = project_with_task["task_id"]
    assert f"endless task spawn E-{tid}" in captured.out
    assert f"eswt E-{tid}" in captured.out

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    branches = subprocess.run(
        ["git", "branch"], cwd=repo, capture_output=True, text=True, check=True,
    ).stdout
    # Only one task branch
    assert branches.count(f"task/{tid}-") == 1


def test_claim_refuses_when_plan_file_uncommitted(project_with_task, capsys):
    from endless.task_cmd import claim_item

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    plans = repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / f"E-{tid}.md").write_text("plan content\n")

    with pytest.raises(click.ClickException) as exc_info:
        claim_item(tid, unattended=True)

    msg = exc_info.value.message
    assert f".endless/plans/E-{tid}.md" in msg
    assert "git -C" in msg
    assert f"endless task claim E-{tid}" in msg

    # No worktree created
    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert not wt.exists()


def test_claim_succeeds_when_plan_file_committed(project_with_task):
    from endless.task_cmd import claim_item

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    plans = repo / ".endless" / "plans"
    plans.mkdir(parents=True)
    (plans / f"E-{tid}.md").write_text("plan content\n")
    _run(["git", "add", f".endless/plans/E-{tid}.md"], repo)
    _run(["git", "commit", "-q", "-m", "add plan"], repo)

    claim_item(tid, unattended=True)

    wt = repo / ".endless" / "worktrees" / f"e-{tid}"
    assert wt.exists()
    # Plan file rides into the worktree
    assert (wt / ".endless" / "plans" / f"E-{tid}.md").exists()


def test_claim_uses_task_fallback_for_all_filler_title(seeded_project_at_cwd):
    from endless.task_cmd import claim_item

    repo = seeded_project_at_cwd
    _run(["git", "init", "-q", "-b", "main"], repo)
    _run(["git", "config", "user.email", "t@t.t"], repo)
    _run(["git", "config", "user.name", "t"], repo)
    (repo / "README.md").write_text("hi\n")
    _run(["git", "add", "README.md"], repo)
    _run(["git", "commit", "-q", "-m", "init"], repo)

    proj_id = db.query("SELECT id FROM projects WHERE path = ?", (str(repo),))[0]["id"]
    db.execute(
        "INSERT INTO tasks (project_id, title, description, status, sort_order, "
        "created_at, updated_at) VALUES (?, ?, ?, 'ready', 0, "
        "datetime('now'), datetime('now'))",
        (proj_id, "The to from", "filler"),
    )
    tid = db.query("SELECT id FROM tasks WHERE title = ?", ("The to from",))[0]["id"]

    claim_item(tid, unattended=True)

    companion = json.loads(
        (repo / ".endless" / "worktrees" / f"e-{tid}" / ".endless" / "worktree.json").read_text()
    )
    assert companion["branch"] == f"task/{tid}-task"


def test_claim_skips_eval_line_when_eswt_already_defined(project_with_task, capsys, monkeypatch):
    """When _eswt_defined_in_user_shell() returns True, suppress the bootstrap line."""
    from endless import task_cmd

    monkeypatch.setattr(task_cmd, "_eswt_defined_in_user_shell", lambda: True)
    task_cmd.claim_item(project_with_task["task_id"], unattended=True)
    captured = capsys.readouterr()
    tid = project_with_task["task_id"]
    assert f"eswt E-{tid}" in captured.out
    assert 'eval "$(endless shell-init)"' not in captured.out


def test_claim_worktree_discoverable_via_for_task(project_with_task):
    """After start, the new worktree shows up in worktree list / for-task."""
    from endless.task_cmd import claim_item
    from endless.worktree_cmd import _branch_for_task, _enriched_list

    claim_item(project_with_task["task_id"], unattended=True)

    repo = project_with_task["project_root"]
    tid = project_with_task["task_id"]
    rows = _enriched_list(repo)
    match = _branch_for_task(rows, f"E-{tid}")
    assert match is not None
    assert match["state"] == "active"
    assert Path(match["path"]) == repo / ".endless" / "worktrees" / f"e-{tid}"
