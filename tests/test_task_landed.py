"""Tests for E-1478: surface task landings in the read CLI.

Covers the `Landed:` line in `task show` (human / --llm / --json) and the
`task landed` command (bare list + per-task history). Landing data lives in
the `task_landings` table, written append-only by `endless worktree land`.
"""

import json

import pytest

from endless import db, task_cmd


def _project_id() -> int:
    rows = db.query("SELECT id FROM projects WHERE name = 'my-project'")
    return rows[0]["id"]


def _insert_task(pk: int, title: str = "Some work", status: str = "ready"):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status, phase) "
        "VALUES (?, ?, ?, ?, 'now')",
        (pk, _project_id(), title, status),
    )


def _insert_landing(task_id: int, sha: str, landed_at: str,
                    branch: str = "task/x"):
    db.execute(
        "INSERT INTO task_landings (task_id, branch, merge_commit_sha, landed_at) "
        "VALUES (?, ?, ?, ?)",
        (task_id, branch, sha, landed_at),
    )


# ---- _format_landed_line (pure) -------------------------------------------

def test_format_landed_line_single_has_no_count_suffix():
    line = task_cmd._format_landed_line(
        [{"merge_commit_sha": "abc1234def", "landed_at": "2026-05-25T16:09:00"}]
    )
    assert "abc1234" in line
    assert "landed" not in line  # no "(landed N times)" suffix for a single land


def test_format_landed_line_multiple_has_count_suffix():
    landings = [
        {"merge_commit_sha": "newsha00", "landed_at": "2026-05-26T10:00:00"},
        {"merge_commit_sha": "oldsha00", "landed_at": "2026-05-25T10:00:00"},
    ]
    line = task_cmd._format_landed_line(landings)
    assert "newsha0" in line          # latest (first) sha, shortened
    assert "(landed 2 times)" in line


def test_format_landed_line_tolerates_missing_sha():
    line = task_cmd._format_landed_line(
        [{"merge_commit_sha": "", "landed_at": "2026-05-25T16:09:00"}]
    )
    assert "2026-05-25" in line  # renders the timestamp, omits the empty sha


# ---- task show: Landed: line ----------------------------------------------

def test_show_human_shows_landed_line_with_count(registered_project, capsys):
    _insert_task(9001)
    _insert_landing(9001, "aaaaaaa1", "2026-05-25T16:09:00")
    _insert_landing(9001, "bbbbbbb2", "2026-05-26T16:09:00")
    task_cmd.detail_item(9001)
    out = capsys.readouterr().out
    assert "Landed:" in out
    assert "bbbbbbb" in out               # latest sha
    assert "(landed 2 times)" in out


def test_show_human_omits_landed_line_when_never_landed(registered_project, capsys):
    _insert_task(9002)
    task_cmd.detail_item(9002)
    out = capsys.readouterr().out
    assert "Landed:" not in out


def test_show_llm_emits_landed_field(registered_project, capsys):
    _insert_task(9003)
    _insert_landing(9003, "ccccccc3", "2026-05-25T16:09:00")
    task_cmd.detail_item(9003, llm=True)
    out = capsys.readouterr().out
    assert "landed=" in out
    assert "ccccccc" in out


def test_show_json_carries_landed_object(registered_project, capsys):
    _insert_task(9004)
    _insert_landing(9004, "ddddddd4", "2026-05-25T16:09:00")
    _insert_landing(9004, "eeeeeee5", "2026-05-26T16:09:00")
    task_cmd.detail_item(9004, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    assert payload["landed"]["count"] == 2
    assert payload["landed"]["merge_commit_sha"] == "eeeeeee5"  # latest, full sha


def test_show_json_landed_null_when_never_landed(registered_project, capsys):
    _insert_task(9005)
    task_cmd.detail_item(9005, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    assert payload["landed"] is None


# ---- task landed: bare list -----------------------------------------------

def test_landed_list_orders_by_most_recent_landing(registered_project, capsys):
    _insert_task(9101, title="Older land")
    _insert_task(9102, title="Newer land")
    _insert_landing(9101, "1111111", "2026-05-20T10:00:00")
    _insert_landing(9102, "2222222", "2026-05-25T10:00:00")
    task_cmd.landed_list(show_all=True)
    out = capsys.readouterr().out
    assert "E-9101" in out and "E-9102" in out
    assert out.index("E-9102") < out.index("E-9101")  # newest first


def test_landed_list_json_includes_count(registered_project, capsys):
    _insert_task(9103, title="Twice")
    _insert_landing(9103, "3333333", "2026-05-20T10:00:00")
    _insert_landing(9103, "4444444", "2026-05-21T10:00:00")
    task_cmd.landed_list(show_all=True, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    row = next(r for r in payload if r["id"] == "E-9103")
    assert row["count"] == 2


def test_landed_list_empty(registered_project, capsys):
    task_cmd.landed_list(show_all=True)
    out = capsys.readouterr().out
    assert "No landed tasks" in out


# ---- task landed <id>: history --------------------------------------------

def test_landed_item_history_newest_first(registered_project, capsys):
    _insert_task(9201, title="Multi-land")
    _insert_landing(9201, "oldddd1", "2026-05-20T10:00:00", branch="task/9201-x")
    _insert_landing(9201, "newwww2", "2026-05-25T10:00:00", branch="task/9201-x")
    task_cmd.landed_item(9201)
    out = capsys.readouterr().out
    assert "newwww2" in out and "oldddd1" in out
    assert out.index("newwww2") < out.index("oldddd1")


def test_landed_item_never_landed_message(registered_project, capsys):
    _insert_task(9202, title="Unlanded")
    task_cmd.landed_item(9202)
    out = capsys.readouterr().out
    assert "Never landed" in out


def test_landed_item_json_lists_all_landings(registered_project, capsys):
    _insert_task(9203, title="History")
    _insert_landing(9203, "sha00001", "2026-05-20T10:00:00")
    _insert_landing(9203, "sha00002", "2026-05-21T10:00:00")
    task_cmd.landed_item(9203, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    assert len(payload["landings"]) == 2
    assert payload["landings"][0]["merge_commit_sha"] == "sha00002"  # newest first
