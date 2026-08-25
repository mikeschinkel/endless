"""Tests for E-1185's second half: `duplicates` as a FIRST-CLASS relation type.

The first landing added the type to the vocabulary, `task link` and the guide,
and stopped there — because `replaces`, its nearest neighbour, stopped there too.
That was the wrong yardstick. Two gaps close here:

1. `--duplicates` / `--replaces` on `task add`, `task update` and `epic add`.
   `task update` had no relation flags at all before this.
2. The inline `(duplicates E-NNN)` note beside a TERMINAL status, on every
   surface that already carries `(replaced by E-NNN)` from E-1956 — `task show`
   and `task list`, human/--llm/--json. The Go `session status` side is covered
   by the Go tests. E-2064 later pulled it back out of the human TABLES — see
   tests/test_status_column_width.py — so `task list`'s human assertion here is
   now the negative one.

The recurring trap, and what most of these tests exist to catch: the two
relations annotate OPPOSITE endpoints. `replaces` notes the target (`new
replaces old`, old is closed); `duplicates` notes the source (`dupe duplicates
keeper`, dupe is closed).
"""

import json

import pytest
from click.testing import CliRunner

from endless import cli, db, task_cmd

_TASK = 1


def _add_task(title: str, status: str = "underway", type_id: int = _TASK) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _link_duplicates(dupe_id: int, keeper_id: int) -> None:
    """Write the relation directly, in its stored active-voice form."""
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, 'duplicates')",
        (dupe_id, keeper_id),
    )


def _duplicate_pair(status: str = "obsolete") -> tuple[int, int]:
    dupe = _add_task("Add the redundant thing", status=status)
    keeper = _add_task("Add the thing that is kept", status="underway")
    _link_duplicates(dupe, keeper)
    return dupe, keeper


def _deps() -> list[tuple[int, int, str]]:
    return [
        (r["source_id"], r["target_id"], r["dep_type"])
        for r in db.query(
            "SELECT source_id, target_id, dep_type FROM task_deps ORDER BY id")
    ]


# ─── the relation lookup ─────────────────────────────────────────────────────


def test_duplicates_map_notes_the_source_not_the_target(seeded_project_at_cwd):
    """The mirror-image assertion. `replaced_by_map` keys on the target; this
    keys on the source, because that is the end that gets closed."""
    dupe, keeper = _duplicate_pair()
    assert task_cmd.duplicates_map([dupe, keeper]) == {dupe: [keeper]}


def test_duplicates_map_is_not_replaced_by_map(seeded_project_at_cwd):
    """A `duplicates` row must be invisible to the supersession lookup, and vice
    versa — the two read the same table with swapped columns."""
    dupe, keeper = _duplicate_pair()
    assert task_cmd.replaced_by_map([dupe, keeper]) == {}

    old = _add_task("Add the superseded thing", status="assumed")
    new = _add_task("Add the replacement", status="underway")
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, 'replaces')", (new, old))
    assert task_cmd.duplicates_map([old, new]) == {}


def test_duplicates_map_is_empty_for_an_empty_id_set(seeded_project_at_cwd):
    assert task_cmd.duplicates_map([]) == {}


def test_duplicates_map_ignores_a_removed_keeper(seeded_project_at_cwd):
    dupe = _add_task("Add the redundant thing", status="obsolete")
    _link_duplicates(dupe, 9999)  # no tasks row — what a removal leaves behind
    assert task_cmd.duplicates_map([dupe]) == {}


def test_duplicates_map_collects_every_keeper(seeded_project_at_cwd):
    dupe = _add_task("Add the redundant thing", status="obsolete")
    a = _add_task("Keeper A")
    b = _add_task("Keeper B")
    _link_duplicates(dupe, a)
    _link_duplicates(dupe, b)
    assert task_cmd.duplicates_map([dupe]) == {dupe: [a, b]}


# ─── the note ────────────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    "status", ["confirmed", "assumed", "completed", "declined", "obsolete"])
def test_note_renders_alongside_a_terminal_status(status):
    assert task_cmd.duplicates_note(status, [7]) == " (duplicates E-7)"


@pytest.mark.parametrize(
    "status", ["untriaged", "unplanned", "submitted", "ready", "underway",
               "unverified", "blocked", "revisit"])
def test_note_is_suppressed_on_an_open_status(status):
    assert task_cmd.duplicates_note(status, [7]) == ""


def test_note_is_empty_without_ids():
    assert task_cmd.duplicates_note("obsolete", None) == ""
    assert task_cmd.duplicates_note("obsolete", []) == ""


def test_note_lists_every_id():
    assert task_cmd.duplicates_note("obsolete", [7, 9]) == " (duplicates E-7, E-9)"


def test_status_notes_compose_without_either_winning():
    """A task can be both superseded and a duplicate."""
    got = task_cmd.status_notes("obsolete", [7], [9])
    assert got == " (replaced by E-7) (duplicates E-9)"


# ─── the rendered surfaces ───────────────────────────────────────────────────


def test_task_show_human_puts_the_note_on_the_status_line(
    seeded_project_at_cwd, capsys
):
    dupe, keeper = _duplicate_pair()
    task_cmd.detail_item(dupe, no_color=True)
    for line in capsys.readouterr().out.splitlines():
        if line.startswith("Status:"):
            assert line.strip() == f"Status:     obsolete (duplicates E-{keeper})"
            return
    pytest.fail("no Status: line in `task show` output")


def test_task_show_human_leaves_the_keeper_alone(seeded_project_at_cwd, capsys):
    """The note names where the work went. On the task that IS the work it would
    be backwards."""
    dupe, keeper = _duplicate_pair()
    task_cmd.detail_item(keeper, no_color=True)
    out = capsys.readouterr().out
    status_line = next(ln for ln in out.splitlines() if ln.startswith("Status:"))
    assert "duplicates" not in status_line


def test_task_show_llm_puts_the_note_on_the_status_line(
    seeded_project_at_cwd, capsys
):
    dupe, keeper = _duplicate_pair()
    task_cmd.detail_item(dupe, llm=True)
    assert f"status=obsolete duplicates=E-{keeper}" in capsys.readouterr().out


def test_task_show_json_emits_the_relation_ungated(seeded_project_at_cwd, capsys):
    # `unverified` is NOT terminal, so the human/--llm note is suppressed — but
    # --json is data, and the relation is emitted anyway.
    dupe, keeper = _duplicate_pair(status="unverified")
    task_cmd.detail_item(dupe, as_json=True)
    assert json.loads(capsys.readouterr().out)["duplicates"] == [f"E-{keeper}"]


def test_task_show_json_always_carries_the_key(seeded_project_at_cwd, capsys):
    tid = _add_task("Add a one-of-a-kind thing", status="assumed")
    task_cmd.detail_item(tid, as_json=True)
    assert json.loads(capsys.readouterr().out)["duplicates"] == []


def test_task_list_renders_the_bare_status(seeded_project_at_cwd, capsys):
    """E-2064: the shared Status column carries the bare status. The sibling
    assertion in test_replaced_by_inline.py covers the other relation."""
    dupe, keeper = _duplicate_pair()
    task_cmd.show_plan(show_all=True)
    out = capsys.readouterr().out
    assert f"E-{dupe}" in out
    assert "(duplicates" not in out


def test_task_list_default_view_is_unchanged(seeded_project_at_cwd, capsys):
    """The default listing excludes terminal statuses, so no row can carry a
    note — the same property that kept E-1956 invisible in normal use."""
    _duplicate_pair()
    _add_task("Add an open thing", status="ready")
    task_cmd.show_plan()
    assert "duplicates" not in capsys.readouterr().out


def test_task_list_llm_puts_the_note_after_the_status(
    seeded_project_at_cwd, capsys
):
    dupe, keeper = _duplicate_pair()
    task_cmd.show_plan(show_all=True, llm=True)
    assert f"obsolete duplicates=E-{keeper}" in capsys.readouterr().out


def test_task_list_json_emits_the_relation_ungated(seeded_project_at_cwd, capsys):
    dupe, keeper = _duplicate_pair(status="unverified")
    task_cmd.show_plan(show_all=True, as_json=True)
    rows = {r["id"]: r for r in json.loads(capsys.readouterr().out)}
    assert rows[f"E-{dupe}"]["duplicates"] == [f"E-{keeper}"]
    assert rows[f"E-{keeper}"]["duplicates"] == []


def test_both_notes_render_together(seeded_project_at_cwd, capsys):
    dupe, keeper = _duplicate_pair()
    other = _add_task("Add the replacement", status="underway")
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, 'replaces')", (other, dupe))
    task_cmd.detail_item(dupe, no_color=True)
    status_line = next(
        ln for ln in capsys.readouterr().out.splitlines() if ln.startswith("Status:"))
    assert f"(replaced by E-{other})" in status_line
    assert f"(duplicates E-{keeper})" in status_line


# ─── the flags ───────────────────────────────────────────────────────────────


def _stub_add(monkeypatch):
    def _stub(title, description=None, text=None, phase="now", project_name=None,
              after=None, parent_id=None, task_type=None, status=None,
              tier=None, force=False, **kwargs):
        cur = db.execute(
            "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
            "VALUES (1, ?, ?, 1, ?, datetime('now'))",
            (title, status or "unplanned", phase))
        return cur.lastrowid
    monkeypatch.setattr(task_cmd, "add_item", _stub)


def test_task_add_duplicates_flag(seeded_project_at_cwd, monkeypatch):
    _stub_add(monkeypatch)
    keeper = _add_task("Add the thing that is kept")
    result = CliRunner().invoke(cli.main, [
        "task", "add", "Add the same thing again", "--duplicates", str(keeper)])
    assert result.exit_code == 0, result.output
    (src, tgt, dep), = _deps()
    assert (tgt, dep) == (keeper, "duplicates")
    assert src != keeper  # the new task is the redundant filing


def test_task_add_replaces_flag(seeded_project_at_cwd, monkeypatch):
    _stub_add(monkeypatch)
    old = _add_task("Add the old thing")
    result = CliRunner().invoke(cli.main, [
        "task", "add", "Add the new thing", "--replaces", str(old)])
    assert result.exit_code == 0, result.output
    (src, tgt, dep), = _deps()
    assert (tgt, dep) == (old, "replaces")
    assert src != old


def test_task_update_records_the_relation_with_no_other_edit(seeded_project_at_cwd):
    """`update_plan` refuses an edit that names no field. A relation flag IS the
    edit, so that refusal must not fire — but must still fire on a bare
    `task update`."""
    dupe = _add_task("Add the redundant thing")
    keeper = _add_task("Add the thing that is kept")
    result = CliRunner().invoke(cli.main, [
        "task", "update", str(dupe), "--duplicates", str(keeper)])
    assert result.exit_code == 0, result.output
    assert _deps() == [(dupe, keeper, "duplicates")]


def test_task_update_with_no_flags_at_all_is_still_refused(seeded_project_at_cwd):
    tid = _add_task("Add a thing")
    result = CliRunner().invoke(cli.main, ["task", "update", str(tid)])
    assert result.exit_code != 0
    assert "Nothing to update" in result.output


def test_task_update_combines_a_field_edit_with_a_relation(seeded_project_at_cwd):
    dupe = _add_task("Add the redundant thing")
    keeper = _add_task("Add the thing that is kept")
    result = CliRunner().invoke(cli.main, [
        "task", "update", str(dupe), "--phase", "later",
        "--duplicates", str(keeper)])
    assert result.exit_code == 0, result.output
    assert _deps() == [(dupe, keeper, "duplicates")]
    assert db.query(
        "SELECT phase FROM tasks WHERE id = ?", (dupe,))[0]["phase"] == "later"


def test_task_update_applies_the_relation_to_every_named_task(seeded_project_at_cwd):
    a = _add_task("Redundant A")
    b = _add_task("Redundant B")
    keeper = _add_task("Add the thing that is kept")
    result = CliRunner().invoke(cli.main, [
        "task", "update", str(a), str(b), "--duplicates", str(keeper)])
    assert result.exit_code == 0, result.output
    assert _deps() == [(a, keeper, "duplicates"), (b, keeper, "duplicates")]


def test_task_update_replaces_flag(seeded_project_at_cwd):
    new = _add_task("Add the new thing")
    old = _add_task("Add the old thing")
    result = CliRunner().invoke(cli.main, [
        "task", "update", str(new), "--replaces", str(old)])
    assert result.exit_code == 0, result.output
    assert _deps() == [(new, old, "replaces")]


def test_task_update_replaces_does_not_close_the_replaced_task(seeded_project_at_cwd):
    """`task replace` is the status-bearing surface; the flag records the
    relation only, and the help text says so."""
    new = _add_task("Add the new thing")
    old = _add_task("Add the old thing", status="ready")
    CliRunner().invoke(cli.main, ["task", "update", str(new), "--replaces", str(old)])
    assert db.query(
        "SELECT status FROM tasks WHERE id = ?", (old,))[0]["status"] == "ready"


@pytest.mark.parametrize("command", [
    ["task", "add", "--help"],
    ["task", "update", "--help"],
    ["epic", "add", "--help"],
])
def test_the_flags_are_advertised(command):
    out = CliRunner().invoke(cli.main, command).output
    assert "--duplicates" in out
    assert "--replaces" in out
    # The trap the help text exists to close.
    assert "task replace" in out
