"""E-1308 (consolidated into E-1940): `worktree land` on a worktree that is gone.

A landed worktree is REMOVED by the reaper once its recorded landing ages past
worktree_ttl, so "no worktree" is the ordinary end state of successful work. The
only message for it was "No endless-managed worktree for E-NNN", which reads as
"your work is lost" — and sends the reader looking for a branch that was already
merged and cleaned up.

E-1308 proposed detecting the state with `git branch --merged`. That cannot
work: `land` REBASES, so a landed branch is not an ancestor of the base branch
and the probe is false for exactly the case it targets. The recorded landing is
the reliable signal — the same one the ◆ probe and the reaper now use.
"""

import pytest

from endless import db, worktree_cmd


def _project_id() -> int:
    return db.query("SELECT id FROM projects WHERE name = 'my-project'")[0]["id"]


def _insert_task(pk: int, title: str = "Landed work"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase) "
        "VALUES (?, ?, ?, 'confirmed', 'now')",
        (pk, _project_id(), title),
    )


def _insert_landing(task_id: int, sha: str, landed_at: str):
    db.execute(
        "INSERT INTO task_landings (task_id, merge_commit_sha, landed_at) "
        "VALUES (?, ?, ?)",
        (task_id, sha, landed_at),
    )


def test_reports_the_recorded_landing_instead_of_implying_loss(registered_project):
    _insert_task(9301)
    _insert_landing(9301, "abc123def4567890", "2026-08-01T10:00:00")

    msg = worktree_cmd._no_worktree_to_land_message("E-9301")

    assert "already landed" in msg
    assert "abc123def456" in msg
    assert "2026-08-01T10:00:00" in msg
    assert "nothing to do" in msg
    # The old message must not survive alongside the new one: reading both would
    # leave the user exactly as unsure as before.
    assert "No endless-managed worktree" not in msg


def test_names_the_latest_landing_when_a_task_landed_more_than_once(
        registered_project):
    # Landing is append-only — a follow-up commit lands again — so the row that
    # answers "where did my work go?" is the newest, not the first.
    _insert_task(9302)
    _insert_landing(9302, "1111111111111111", "2026-07-01T09:00:00")
    _insert_landing(9302, "2222222222222222", "2026-08-01T09:00:00")

    msg = worktree_cmd._no_worktree_to_land_message("E-9302")

    assert "222222222222" in msg
    assert "111111111111" not in msg


def test_falls_back_when_the_work_is_genuinely_unaccounted_for(registered_project):
    # No worktree AND no landing is the case the original message was right
    # about, and it must keep saying so — the fix is to stop saying it when a
    # landing IS recorded, not to stop saying it at all.
    _insert_task(9303)

    msg = worktree_cmd._no_worktree_to_land_message("E-9303")

    assert "No endless-managed worktree" in msg
    assert "no landing is recorded" in msg
    assert "already landed" not in msg
