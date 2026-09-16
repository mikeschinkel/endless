"""Tests for E-1911 part 4: `task show --children` lists EVERY direct child.

The three render paths (human, --agent, --json) each carried
`AND status != 'confirmed'` in their child query. It excluded exactly one
status, so an epic rendered its `obsolete` and `declined` children while
dropping the ones that were verified and landed — E-1906 vanished from
`task show E-1785 --children` while four obsolete children stayed. Confirmed
children are precisely what an epic nearing completion needs to show, so the
filter is gone and these pin it staying gone.
"""

import json

from endless import db, task_cmd


def _project_id() -> int:
    rows = db.query("SELECT id FROM projects WHERE name = 'my-project'")
    return rows[0]["id"]


def _insert_task(pk: int, title: str, status: str, parent: int | None = None):
    db.execute(
        "INSERT INTO tasks (id, project_id, parent_id, title, status, phase) "
        "VALUES (?, ?, ?, ?, ?, 'now')",
        (pk, _project_id(), parent, title, status),
    )


def _seed_epic() -> int:
    """An epic with one child per interesting terminal/open status."""
    _insert_task(8800, "The epic", "underway")
    _insert_task(8801, "Verified and landed", "confirmed", parent=8800)
    _insert_task(8802, "Superseded", "obsolete", parent=8800)
    _insert_task(8803, "Believed done", "assumed", parent=8800)
    _insert_task(8804, "Still open", "ready", parent=8800)
    return 8800


def test_human_lists_the_confirmed_child(registered_project, capsys):
    task_cmd.detail_item(_seed_epic(), show_children=True, no_color=True)
    out = capsys.readouterr().out
    for eid in ("E-8801", "E-8802", "E-8803", "E-8804"):
        assert eid in out, f"{eid} missing from --children:\n{out}"


def test_agent_lists_the_confirmed_child(registered_project, capsys):
    task_cmd.detail_item(_seed_epic(), show_children=True, agent=True)
    out = capsys.readouterr().out
    assert "E-8801 now confirmed Verified and landed" in out


def test_json_lists_the_confirmed_child(registered_project, capsys):
    task_cmd.detail_item(_seed_epic(), show_children=True, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    by_id = {c["id"]: c["status"] for c in payload["children"]}
    assert by_id == {
        "E-8801": "confirmed",
        "E-8802": "obsolete",
        "E-8803": "assumed",
        "E-8804": "ready",
    }


def test_confirmed_only_epic_is_not_rendered_as_childless(registered_project, capsys):
    """The pathological case the filter produced: an epic whose children are ALL
    done read as having none, which is the opposite of what it means."""
    _insert_task(8810, "Finished epic", "unverified")
    _insert_task(8811, "Done", "confirmed", parent=8810)
    task_cmd.detail_item(8810, show_children=True, no_color=True)
    out = capsys.readouterr().out
    assert "E-8811" in out
    assert "(none)" not in out
