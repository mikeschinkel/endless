"""The advisory relations E-2164 adds: `precedes` and `conflicts_with`.

Both are recorded data a reader (and `session status`'s ordering graph) can
see, and neither is a block: `task next` still offers either end.
"""

from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main

from test_relations import _add_dep, _add_task, _seed_project, _seed_project_at_cwd


def _invoke(*args):
    result = CliRunner().invoke(main, list(args))
    assert result.exit_code == 0, result.output
    return result.output


def _deps():
    return [dict(r) for r in db.query(
        "SELECT source_id, target_id, dep_type FROM task_deps ORDER BY id")]


def test_registries_carry_both_relations():
    assert task_cmd.CANONICAL_DEP_TYPES["precedes"] == ("precedes", False)
    assert task_cmd.CANONICAL_DEP_TYPES["preceded_by"] == ("precedes", True)
    assert task_cmd.CANONICAL_DEP_TYPES["conflicts_with"] == ("conflicts_with", False)
    # Symmetric: one stored row, no inverse view name.
    assert "conflicted_by" not in task_cmd.CANONICAL_DEP_TYPES
    assert "conflicts_with" in task_cmd.SYMMETRIC_DEP_TYPES
    for stored in ("precedes", "conflicts_with"):
        assert stored in task_cmd.STORED_DEP_TYPES
    assert task_cmd.RELATION_LABELS["precedes"] == "Should precede"
    assert task_cmd.RELATION_LABELS["preceded_by"] == "Should follow"
    assert task_cmd.RELATION_LABELS["conflicts_with"] == "Conflicts with"


def test_display_order_sits_below_blocking_rows():
    order = task_cmd.RELATION_DISPLAY_ORDER
    last_block = max(order.index("blocked_by"), order.index("blocks"))
    for name in ("precedes", "preceded_by", "conflicts_with"):
        assert order.index(name) > last_block, name
        assert order.index(name) < order.index("implements"), name


def test_link_precedes_and_preceded_by(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a, b, c = _add_task("A"), _add_task("B"), _add_task("C")
    task_cmd.link_tasks(a, b, "precedes")
    task_cmd.link_tasks(c, a, "preceded_by")  # stored as A precedes C
    assert _deps() == [
        {"source_id": a, "target_id": b, "dep_type": "precedes"},
        {"source_id": a, "target_id": c, "dep_type": "precedes"},
    ]


def test_conflicts_with_is_one_row_from_either_end(isolated_env, monkeypatch):
    import click
    import pytest

    _seed_project_at_cwd(monkeypatch, isolated_env)
    a, b = _add_task("A"), _add_task("B")
    task_cmd.link_tasks(a, b, "conflicts_with")
    # The reverse spelling is the same fact, so it is a duplicate.
    with pytest.raises(click.ClickException, match="already linked"):
        task_cmd.link_tasks(b, a, "conflicts_with")
    assert len(_deps()) == 1
    # Unlinking from the OTHER end removes the stored row.
    task_cmd.unlink_tasks(b, a, "conflicts_with")
    assert _deps() == []


def test_task_add_flags(isolated_env, monkeypatch):
    _seed_project_at_cwd(monkeypatch, isolated_env)
    a, b, c = _add_task("A"), _add_task("B"), _add_task("C")

    def _stub(title, **kwargs):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, description, status, type_id, phase, created_at) "
            "VALUES (1, ?, 'x', 'unplanned', 1, 'now', datetime('now'))", (title,))
        return cur.lastrowid

    monkeypatch.setattr(task_cmd, "add_item", _stub)
    _invoke("task", "add", "Do the thing", "--precedes", str(a),
            "--preceded-by", str(b), "--conflicts-with", str(c))
    new = db.query("SELECT max(id) AS id FROM tasks")[0]["id"]
    got = {(r["source_id"], r["target_id"], r["dep_type"]) for r in _deps()}
    assert got == {
        (new, a, "precedes"),
        (b, new, "precedes"),
        (new, c, "conflicts_with"),
    }


def test_task_show_labels(isolated_env):
    _seed_project()
    a, b, c = _add_task("A"), _add_task("B"), _add_task("C")
    _add_dep(a, b, "precedes")
    _add_dep(a, c, "conflicts_with")

    out_a = _invoke("task", "show", str(a))
    assert "Should precede:" in out_a and f"E-{b} [ready]" in out_a
    assert "Conflicts with:" in out_a and f"E-{c} [ready]" in out_a
    assert "Blocks:" not in out_a

    out_b = _invoke("task", "show", str(b))
    assert "Should follow:" in out_b and f"E-{a} [ready]" in out_b
    assert "Blocked by:" not in out_b

    # One stored row renders on BOTH tasks under the same label.
    out_c = _invoke("task", "show", str(c))
    assert "Conflicts with:" in out_c and f"E-{a} [ready]" in out_c


def test_neither_relation_stops_task_next(isolated_env):
    _seed_project()
    a, b, c = _add_task("Alpha one"), _add_task("Beta two"), _add_task("Gamma three")
    _add_dep(a, b, "precedes")
    _add_dep(a, c, "conflicts_with")
    out = _invoke("task", "next", "--all", "--json")
    for tid in (a, b, c):
        assert f"E-{tid}" in out or f'"id": {tid}' in out, (tid, out)


def test_task_link_help_lists_both():
    out = _invoke("task", "link", "--help")
    for name in ("precedes", "preceded_by", "conflicts_with"):
        assert name in out, name
