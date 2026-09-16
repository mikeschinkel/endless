"""Tests for E-2064: the supersession note is a DETAIL-view annotation.

E-1956 put ` (replaced by E-NNN)` beside a terminal status, and E-1185 added
` (duplicates E-NNN)` on the same rule. Both landed on every surface that shows
a status — including the human tables, where the cost is not what it is in a
detail view.

A detail view has ONE status and unlimited width, so an annotation there is
free. A table has many rows sharing ONE Status column, so the column is sized by
its longest cell: 'obsolete (replaced by E-1367)' is roughly three times a bare
status, and the difference comes out of every row's Title. A handful of
annotated rows were charging the whole table for a fact any reader recovers by
opening the task.

So the invariant these tests pin is not "the note is absent" — it is stronger
and says why: **adding the relation must not change the human table at all.**
Byte-identity is the assertion, because width, truncation and alignment are
exactly what the defect moved. The machine-readable modes and the detail views
are asserted in the opposite direction: they keep the note, so this fix cannot
be mistaken for undoing E-1956.
"""

import json

from endless import db, decision_cmd, task_cmd


# ─── fixtures ────────────────────────────────────────────────────────────────


def _add_task(title: str, status: str = "underway") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _link(source_id: int, target_id: int, dep_type: str) -> None:
    db.execute(
        "INSERT INTO task_deps "
        "(source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, ?)",
        (source_id, target_id, dep_type),
    )


def _seed_rows() -> tuple[int, int]:
    """A terminal row and an open row, with titles long enough that any width
    stolen from the Title column shows up as truncation."""
    closed = _add_task(
        "Consolidate the Go binaries into one unified entry point",
        status="obsolete")
    open_ = _add_task(
        "Move the task display reads from Python over to Go", status="ready")
    return closed, open_


def _list_table(capsys) -> str:
    task_cmd.show_plan(show_all=True)
    return capsys.readouterr().out


# ─── the task table ──────────────────────────────────────────────────────────


def test_replaced_by_does_not_change_the_task_table(seeded_project_at_cwd, capsys):
    # Every row exists in BOTH renders. The only difference is the relation, so
    # any difference in the output is the relation's cost — and there must be
    # none. Before this fix the Status column grew from 8 to 26 columns and both
    # long titles above truncated to pay for it.
    closed, _ = _seed_rows()
    replacement = _add_task("The replacement")
    before = _list_table(capsys)

    _link(replacement, closed, "replaces")
    after = _list_table(capsys)

    assert after == before
    assert "replaced by" not in after


def test_duplicates_does_not_change_the_task_table(seeded_project_at_cwd, capsys):
    closed, _ = _seed_rows()
    keeper = _add_task("The keeper")
    before = _list_table(capsys)

    _link(closed, keeper, "duplicates")
    after = _list_table(capsys)

    assert after == before
    assert "(duplicates" not in after


def test_the_status_column_is_sized_by_the_longest_bare_status(
    seeded_project_at_cwd, capsys
):
    """The width claim, asserted directly rather than by comparison: the header
    rule under 'Status' is as wide as the widest status word and no wider."""
    closed, _ = _seed_rows()
    _link(_add_task("The replacement"), closed, "replaces")

    lines = _list_table(capsys).splitlines()
    header = next(ln for ln in lines if ln.startswith("ID "))
    sep = lines[lines.index(header) + 1]
    # Columns are '  '-separated in both rows, so the separator's third run of
    # '─' is the Status column's rendered width.
    status_rule = sep.split("  ")[2]
    statuses = {
        r["status"] for r in db.query("SELECT status FROM tasks")
    }
    assert len(status_rule) == max(len(s) for s in statuses)


def test_a_row_that_is_both_stays_bare(seeded_project_at_cwd, capsys):
    """Composed notes were the widest cell of all — the case that cost the most."""
    closed, _ = _seed_rows()
    _link(_add_task("The replacement"), closed, "replaces")
    _link(closed, _add_task("The keeper"), "duplicates")

    out = _list_table(capsys)
    assert f"E-{closed}" in out
    assert "replaced by" not in out
    assert "(duplicates" not in out


def test_task_show_children_table_is_bare_too(seeded_project_at_cwd, capsys):
    """`task show --children` renders through the same table, and it is where
    the defect was reported."""
    parent = _add_task("The parent", status="underway")
    child = _add_task("The superseded child", status="obsolete")
    db.execute("UPDATE tasks SET parent_id = ? WHERE id = ?", (parent, child))
    _link(_add_task("The replacement"), child, "replaces")

    task_cmd.detail_item(parent, show_children=True, no_color=True)
    out = capsys.readouterr().out
    assert f"E-{child}" in out
    assert "replaced by" not in out


# ─── what must NOT change ────────────────────────────────────────────────────


def test_task_show_keeps_the_note(seeded_project_at_cwd, capsys):
    """The detail view is where E-1956 asked for it, and it stays."""
    closed, _ = _seed_rows()
    new = _add_task("The replacement")
    _link(new, closed, "replaces")

    task_cmd.detail_item(closed, no_color=True)
    status_line = next(
        ln for ln in capsys.readouterr().out.splitlines()
        if ln.startswith("Status:"))
    assert f"(replaced by E-{new})" in status_line


def test_task_list_agent_keeps_the_note(seeded_project_at_cwd, capsys):
    """--agent is one line per row with no shared column, so it pays no width."""
    closed, _ = _seed_rows()
    new = _add_task("The replacement")
    _link(new, closed, "replaces")

    task_cmd.show_plan(show_all=True, agent=True)
    assert f"obsolete replaced_by=E-{new}" in capsys.readouterr().out


def test_task_list_json_keeps_the_relation(seeded_project_at_cwd, capsys):
    closed, _ = _seed_rows()
    new = _add_task("The replacement")
    _link(new, closed, "replaces")

    task_cmd.show_plan(show_all=True, as_json=True)
    rows = {r["id"]: r for r in json.loads(capsys.readouterr().out)}
    assert rows[f"E-{closed}"]["replaced_by"] == [f"E-{new}"]


# ─── the decision table, same defect ─────────────────────────────────────────


def _add_decision(title: str, status: str = "accepted") -> int:
    pid = db.query("SELECT id FROM projects WHERE name = 'test'")[0]["id"]
    cur = db.execute(
        "INSERT INTO decisions (project_id, title, description, status, "
        "created_at, updated_at) "
        "VALUES (?, ?, '', ?, datetime('now'), datetime('now'))",
        (pid, title, status),
    )
    return cur.lastrowid


def _link_supersedes(new_id: int, old_id: int) -> None:
    db.execute(
        "INSERT INTO decision_relations "
        "(source_decision_id, target_kind, target_id, relation_type) "
        "VALUES (?, 'decision', ?, 'supersedes')",
        (new_id, old_id),
    )


def test_superseded_by_does_not_change_the_decision_table(
    seeded_project_at_cwd, capsys
):
    """`decision list` carries the sibling ' (by ED-NNN)' annotation in a shared
    Status column — the identical defect, folded into E-2064."""
    old = _add_decision(
        "The rule that governed how binaries were built and named",
        status="superseded")
    successor = _add_decision("The rule that took over from it")

    decision_cmd.list_decisions()
    before = capsys.readouterr().out

    _link_supersedes(successor, old)
    decision_cmd.list_decisions()
    after = capsys.readouterr().out

    assert after == before
    assert "(by ED-" not in after


def test_decision_list_agent_keeps_the_note(seeded_project_at_cwd, capsys):
    old = _add_decision("The rule that was retired", status="superseded")
    new = _add_decision("The rule that took over")
    _link_supersedes(new, old)

    decision_cmd.list_decisions(agent=True)
    assert f"superseded (by ED-{new})" in capsys.readouterr().out


def test_decision_list_json_keeps_the_relation(seeded_project_at_cwd, capsys):
    old = _add_decision("The rule that was retired", status="superseded")
    new = _add_decision("The rule that took over")
    _link_supersedes(new, old)

    decision_cmd.list_decisions(as_json=True)
    rows = {r["id"]: r for r in json.loads(capsys.readouterr().out)}
    assert rows[f"ED-{old}"]["superseded_by"] == [f"ED-{new}"]
