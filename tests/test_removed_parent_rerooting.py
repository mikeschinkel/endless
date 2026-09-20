"""Tests for E-2161: a live child of a removed parent renders under its
nearest live ancestor.

Removal retains the row (E-1929) and now retains the EDGE too — the
`UPDATE tasks SET parent_id = NULL WHERE parent_id = ?` that removal ran is
gone. That null was load-bearing for exactly
one thing: every tree read joined `live_tasks` to `live_tasks`, so a live child
left pointing at a hidden row would hang off nothing and disappear from every
render. Keeping the edge is only safe because the reads adopt the child upward
instead, through `task_tree.effective_parent_id`.

These pin the user-visible half of that: what the child hangs off in each render
path. The write-side half (that `parent_id` itself survives) and the view's own
per-shape contract live in Go, in internal/events/task_removal_test.go.

The distinction every assertion here turns on: `parent_id` answers "what did the
user set" and still reports the removed parent on the `Parent:` line;
`effective_parent_id` answers "where does this render".
"""

import json

from endless import db, task_cmd


def _project_id() -> int:
    rows = db.query("SELECT id FROM projects WHERE name = 'my-project'")
    return rows[0]["id"]


def _insert_task(pk: int, title: str, status: str = "ready",
                 parent: int | None = None, removed: int = 0):
    db.execute(
        "INSERT INTO tasks (id, project_id, parent_id, title, status, phase, removed) "
        "VALUES (?, ?, ?, ?, ?, 'now', ?)",
        (pk, _project_id(), parent, title, status, removed),
    )


def _seed_orphaned_branch():
    """Grandparent 9100 → removed middle 9101 → live child 9102.

    Plus 9103, an ordinary live child of the grandparent, so every assertion can
    tell "the adopted child showed up" apart from "everything showed up".
    """
    _insert_task(9100, "Grandparent epic", status="underway")
    _insert_task(9101, "Removed middle", parent=9100, removed=1)
    _insert_task(9102, "Adopted child", parent=9101)
    _insert_task(9103, "Ordinary child", parent=9100)


def test_human_children_show_the_adopted_child(registered_project, capsys):
    _seed_orphaned_branch()
    task_cmd.detail_item(9100, show_children=True, no_color=True)
    out = capsys.readouterr().out
    assert "E-9102" in out, f"the adopted child is missing:\n{out}"
    assert "E-9103" in out
    assert "E-9101" not in out, "the removed middle must stay hidden"


def test_json_children_show_the_adopted_child(registered_project, capsys):
    _seed_orphaned_branch()
    task_cmd.detail_item(9100, show_children=True, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    assert {c["id"] for c in payload["children"]} == {"E-9102", "E-9103"}
    assert payload["children_count"] == 2


def test_agent_children_show_the_adopted_child(registered_project, capsys):
    _seed_orphaned_branch()
    task_cmd.detail_item(9100, show_children=True, agent=True)
    out = capsys.readouterr().out
    assert "E-9102" in out, f"the adopted child is missing:\n{out}"


def test_parent_line_still_reports_the_literal_parent(registered_project, capsys):
    """The two notions are not merged. What the user SET is still what the child's
    own detail reports; only where it RENDERS moved."""
    _seed_orphaned_branch()
    task_cmd.detail_item(9102, as_json=True)
    assert json.loads(capsys.readouterr().out)["parent"] == "E-9101"


def test_list_filters_by_where_the_task_renders(registered_project, capsys):
    """`--parent` selects on the rendered tree, so the adopted child is listed
    under the ancestor it is shown under rather than under a row nothing can see."""
    _seed_orphaned_branch()
    task_cmd.show_plan(project_name="my-project", parent_id=9100)
    out = capsys.readouterr().out
    assert "E-9102" in out, f"the adopted child is missing from the listing:\n{out}"
    assert "E-9103" in out


def test_list_parent_none_means_shown_at_the_root(registered_project, capsys):
    """A child with NO live ancestor at all renders at the top level, so
    `--parent none` has to find it — otherwise it is reachable from nowhere."""
    _insert_task(9110, "Removed root", removed=1)
    _insert_task(9111, "Stranded child", parent=9110)
    task_cmd.show_plan(project_name="my-project", parent_id=task_cmd.PARENT_NONE)
    out = capsys.readouterr().out
    assert "E-9111" in out, f"a task with no live ancestor vanished from the root:\n{out}"


def test_next_treats_the_adopted_child_as_the_leaf(registered_project, capsys):
    """`task next` offers actionable LEAVES. The grandparent is not one — it has a
    live descendant again — and the adopted child is."""
    _seed_orphaned_branch()
    task_cmd.next_tasks(project_name="my-project")
    out = capsys.readouterr().out
    assert "E-9102" in out, f"the adopted child is not offered as a leaf:\n{out}"
    assert "E-9100" not in out, "a task with a live descendant is not a leaf"


def test_removed_listing_still_reads_the_literal_parent(registered_project, capsys):
    """`task list --removed` exists to render removed rows, which `task_tree`
    excludes by construction. It keeps reading `tasks` and filtering on the
    literal parent_id — the only parentage a removed row has."""
    _seed_orphaned_branch()
    task_cmd.show_plan(project_name="my-project", parent_id=9100, removed_only=True)
    out = capsys.readouterr().out
    assert "E-9101" in out, f"the removed child is missing from --removed:\n{out}"
