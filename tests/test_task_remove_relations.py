"""E-1915: `task remove` refuses while relations still reference the task, and
reconcile clears the rows earlier removals already orphaned.

`task_deps` and `decision_relations` cannot declare a foreign key on their task
endpoint (SQLite cannot express an FK whose target table varies by row), so a
task delete used to leave both behind. Nothing surfaced the orphan while the id
stayed free — every consumer joins through `tasks` — but task ids are reused, so
a later task taking the freed id silently inherited the dead relations and
reported them as computed fact.

Every test here stops before the `task.deleted` event, so none of them need the
Go executor. The end-to-end removal path is covered by tests/tasks/e-1915-verify.sh.
"""

import click
import pytest

from endless import db, task_cmd
from endless.reconcile import repair_orphan_relations
from endless.task_cmd import STORED_DEP_TYPES


def _seed_project():
    db.execute(
        "INSERT INTO projects (name, path, status, created_at, updated_at) "
        "VALUES ('test', '/tmp/test', 'active', datetime('now'), datetime('now'))"
    )


def _add_task(title: str, parent_id: int | None = None) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, parent_id, created_at) "
        "VALUES (1, ?, 'ready', 1, 'now', ?, datetime('now'))",
        (title, parent_id),
    )
    return cur.lastrowid


def _add_dep(source_id: int, target_id: int, dep_type: str,
             source_type: str = "task", target_type: str = "task"):
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES (?, ?, ?, ?, ?)",
        (source_type, source_id, target_type, target_id, dep_type),
    )


def _add_decision(title: str = "D") -> int:
    cur = db.execute(
        "INSERT INTO decisions (project_id, title, status, created_at) "
        "VALUES (1, ?, 'accepted', datetime('now'))",
        (title,),
    )
    return cur.lastrowid


def _add_decision_relation(decision_id: int, task_id: int, relation_type: str):
    db.execute(
        "INSERT INTO decision_relations "
        "(source_decision_id, target_kind, target_id, relation_type) "
        "VALUES (?, 'task', ?, ?)",
        (decision_id, task_id, relation_type),
    )


# ── the refusal ──────────────────────────────────────────────────────────────


def test_remove_refused_when_task_is_the_source(isolated_env):
    _seed_project()
    a, b = _add_task("A"), _add_task("B")
    _add_dep(a, b, "cleans_up")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(a)

    assert f"endless task unlink E-{a} --to E-{b} --type cleans_up" in str(exc.value)
    assert db.scalar("SELECT count(*) FROM tasks WHERE id = ?", (a,)) == 1


def test_remove_refused_when_task_is_the_target(isolated_env):
    """The half a naive `source_id = ?` check misses."""
    _seed_project()
    a, b = _add_task("A"), _add_task("B")
    _add_dep(a, b, "blocks")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(b)

    assert f"endless task unlink E-{a} --to E-{b} --type blocks" in str(exc.value)


@pytest.mark.parametrize("dep_type", STORED_DEP_TYPES)
def test_remove_refused_for_every_stored_type(isolated_env, dep_type):
    """No per-type exemption — `relates_to` included. One rule that always
    holds beats two rules with a judgment call at the boundary."""
    _seed_project()
    a, b = _add_task("A"), _add_task("B")
    _add_dep(a, b, dep_type)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(a)

    assert f"--type {dep_type}" in str(exc.value)


def test_remove_refused_by_a_task_to_decision_relation(isolated_env):
    _seed_project()
    a = _add_task("A")
    d = _add_decision()
    _add_dep(a, d, "implements", target_type="decision")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(a)

    assert f"endless task unlink E-{a} --to ED-{d} --type implements" in str(exc.value)


def test_remove_refused_by_a_decision_to_task_relation(isolated_env):
    """decision_relations has the same missing-FK exposure as task_deps, and is
    cleared from the decision's side."""
    _seed_project()
    a = _add_task("A")
    d = _add_decision()
    _add_decision_relation(d, a, "documents")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(a)

    assert f"endless decision unlink ED-{d} --to E-{a} --type documents" in str(exc.value)


def test_refusal_lists_every_relation(isolated_env):
    _seed_project()
    a, b, c = _add_task("A"), _add_task("B"), _add_task("C")
    d = _add_decision()
    _add_dep(a, b, "cleans_up")
    _add_dep(c, a, "blocks")
    _add_decision_relation(d, a, "documents")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(a)
    msg = str(exc.value)

    assert "3 relation(s)" in msg
    assert f"endless task unlink E-{a} --to E-{b} --type cleans_up" in msg
    assert f"endless task unlink E-{c} --to E-{a} --type blocks" in msg
    assert f"endless decision unlink ED-{d} --to E-{a} --type documents" in msg


# ── --cascade ────────────────────────────────────────────────────────────────


def test_cascade_refused_when_a_descendant_holds_the_relation(isolated_env):
    """Checking only the root would let a parent removal bypass the guard and
    delete the child with its relations orphaned exactly as before."""
    _seed_project()
    parent = _add_task("Parent")
    child = _add_task("Child", parent_id=parent)
    grandchild = _add_task("Grandchild", parent_id=child)
    other = _add_task("Other")
    _add_dep(grandchild, other, "relates_to")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(parent, cascade=True)
    msg = str(exc.value)

    # The descendant is named, not just the root the operator typed.
    assert f"E-{grandchild}:" in msg
    assert f"endless task unlink E-{grandchild} --to E-{other} --type relates_to" in msg
    assert db.scalar("SELECT count(*) FROM tasks WHERE id = ?", (grandchild,)) == 1


def test_cascade_attributes_each_row_to_its_own_descendant(isolated_env):
    _seed_project()
    parent = _add_task("Parent")
    c1 = _add_task("C1", parent_id=parent)
    c2 = _add_task("C2", parent_id=parent)
    other = _add_task("Other")
    _add_dep(c1, other, "blocks")
    _add_dep(c2, other, "relates_to")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(parent, cascade=True)
    msg = str(exc.value)

    assert f"E-{c1}:" in msg and f"E-{c2}:" in msg


def test_relation_between_two_cascaded_tasks_still_refuses(isolated_env):
    """Both endpoints are being deleted, so the row would be orphaned with no
    surviving task at all — still a refusal, not a silent sweep."""
    _seed_project()
    parent = _add_task("Parent")
    c1 = _add_task("C1", parent_id=parent)
    c2 = _add_task("C2", parent_id=parent)
    _add_dep(c1, c2, "blocks")

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(parent, cascade=True)

    assert f"endless task unlink E-{c1} --to E-{c2} --type blocks" in str(exc.value)


def test_a_relation_on_an_unrelated_task_does_not_block(isolated_env):
    _seed_project()
    a = _add_task("A")
    b, c = _add_task("B"), _add_task("C")
    _add_dep(b, c, "blocks")

    # Nothing references A, so the guard must not fire.
    assert task_cmd._relations_referencing([a]) == []


# ── regression: the pre-existing child guard is unchanged ────────────────────


def test_children_without_relations_still_demand_cascade(isolated_env):
    _seed_project()
    parent = _add_task("Parent")
    _add_task("Child", parent_id=parent)

    with pytest.raises(click.ClickException) as exc:
        task_cmd.remove_item(parent)

    assert "--cascade" in str(exc.value)


# ── the repair ───────────────────────────────────────────────────────────────


def test_repair_deletes_orphans_and_reports_them(isolated_env, capsys):
    _seed_project()
    live = _add_task("Live")
    d = _add_decision()
    gone = 9999          # an id no tasks row holds
    _add_dep(gone, live, "blocks")
    _add_dep(live, gone, "cleans_up")
    _add_decision_relation(d, gone, "documents")

    assert repair_orphan_relations() == 3
    out = capsys.readouterr().out

    assert "3 orphaned relation row(s)" in out
    assert f"E-{gone} blocks E-{live}" in out
    assert f"ED-{d} documents E-{gone}" in out
    assert db.scalar("SELECT count(*) FROM task_deps") == 0
    assert db.scalar("SELECT count(*) FROM decision_relations") == 0


def test_repair_leaves_live_relations_alone(isolated_env, capsys):
    _seed_project()
    a, b = _add_task("A"), _add_task("B")
    d = _add_decision()
    _add_dep(a, b, "blocks")
    _add_dep(a, d, "implements", target_type="decision")
    _add_decision_relation(d, a, "documents")

    assert repair_orphan_relations() == 0
    assert capsys.readouterr().out == ""
    assert db.scalar("SELECT count(*) FROM task_deps") == 2
    assert db.scalar("SELECT count(*) FROM decision_relations") == 1


def test_repair_ignores_decision_endpoints(isolated_env):
    """A task→decision row is orphanable only through its TASK endpoint —
    decisions are never deleted, and the repair must not treat a decision id
    that happens to be absent from `tasks` as a missing task."""
    _seed_project()
    a = _add_task("A")
    d = _add_decision()
    _add_dep(a, d, "implements", target_type="decision")

    assert repair_orphan_relations() == 0
    assert db.scalar("SELECT count(*) FROM task_deps") == 1


def test_repair_runs_as_part_of_reconcile(isolated_env):
    from endless.reconcile import reconcile

    _seed_project()
    live = _add_task("Live")
    _add_dep(9999, live, "blocks")

    reconcile()

    assert db.scalar("SELECT count(*) FROM task_deps") == 0
