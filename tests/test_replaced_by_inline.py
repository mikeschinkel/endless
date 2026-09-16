"""Tests for E-1956: `replaced_by` inline with terminal status, `obsolete` guarded.

Three things ship here and all three trace to one root cause — `obsolete` reads
as "never happened", and nothing on a status display said otherwise:

1. The supersession renders inline with a TERMINAL status wherever status is
   shown (`task show`, `task list`, and their --agent/--json modes; the Go
   `session status` side is covered by the Go tests). E-2064 later pulled it
   back out of the human TABLES — see tests/test_status_column_width.py — so
   `task list`'s human assertion here is now the negative one.
2. `obsolete` is refused on work that already shipped, pointing at
   `task replace`, which records the relation and keeps the earned status.
3. The task-status vocabulary lives in ONE list (`endless.statuses`), which is
   what stopped `task update --help` from advertising 11 of 13 statuses while
   `update_plan` rejected a 12th.
"""

import json

import click
import pytest

from endless import cli, db, statuses, task_cmd

_TASK = 1


def _add_task(title: str, status: str = "underway", type_id: int = _TASK) -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, ?, 'now', datetime('now'))",
        (title, status, type_id),
    )
    return cur.lastrowid


def _link_replaced_by(old_id: int, new_id: int) -> None:
    """Write the relation directly: `old replaced_by new` is stored active-voice
    as (source=new, target=old, dep_type='replaces')."""
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'task', ?, 'replaces')",
        (new_id, old_id),
    )


def _status(task_id: int) -> str:
    return db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))[0]["status"]


# ─── the relation lookup ─────────────────────────────────────────────────────


def test_replaced_by_map_reads_the_stored_direction(seeded_project_at_cwd):
    old = _add_task("Add the old thing", status="assumed")
    new = _add_task("Add the new thing", status="underway")
    _link_replaced_by(old, new)
    # The OLD task is the one that was replaced; the new one replaces nothing.
    assert task_cmd.replaced_by_map([old, new]) == {old: [new]}


def test_replaced_by_map_is_empty_for_an_empty_id_set(seeded_project_at_cwd):
    # Guards the short-circuit: an empty `IN ()` is a SQL error.
    assert task_cmd.replaced_by_map([]) == {}


def test_replaced_by_map_ignores_a_removed_replacement(seeded_project_at_cwd):
    old = _add_task("Add the old thing", status="assumed")
    new = _add_task("Add the new thing", status="underway")
    _link_replaced_by(old, new)
    db.execute("UPDATE tasks SET removed = 1 WHERE id = ?", (new,))
    assert task_cmd.replaced_by_map([old]) == {}


def test_replaced_by_map_collects_every_replacement(seeded_project_at_cwd):
    old = _add_task("Add the old thing", status="assumed")
    a = _add_task("Add replacement A", status="underway")
    b = _add_task("Add replacement B", status="underway")
    _link_replaced_by(old, a)
    _link_replaced_by(old, b)
    assert task_cmd.replaced_by_map([old]) == {old: [a, b]}


# ─── the terminal-status gate on the note ────────────────────────────────────


@pytest.mark.parametrize(
    "status", ["confirmed", "assumed", "completed", "declined", "obsolete"]
)
def test_note_renders_for_every_terminal_status(status):
    assert task_cmd.replaced_by_note(status, [7]) == " (replaced by E-7)"


@pytest.mark.parametrize(
    "status", ["untriaged", "unplanned", "submitted", "ready", "underway",
               "unverified", "revisit"]
)
def test_note_is_silent_for_a_non_terminal_status(status):
    # An open task's replaced_by still shows in `task show`'s relations block;
    # keeping it off the status line is what leaves default listings unchanged.
    assert task_cmd.replaced_by_note(status, [7]) == ""


def test_note_is_silent_with_no_replacement():
    assert task_cmd.replaced_by_note("assumed", None) == ""
    assert task_cmd.replaced_by_note("assumed", []) == ""


def test_note_lists_every_replacement():
    assert task_cmd.replaced_by_note("assumed", [7, 9]) == " (replaced by E-7, E-9)"


# ─── the rendered surfaces ───────────────────────────────────────────────────


def _superseded_pair(status: str = "assumed") -> tuple[int, int]:
    old = _add_task("Add the superseded thing", status=status)
    new = _add_task("Add the replacement thing", status="underway")
    _link_replaced_by(old, new)
    return old, new


def test_task_show_human_puts_the_note_on_the_status_line(
    seeded_project_at_cwd, capsys
):
    old, new = _superseded_pair()
    task_cmd.detail_item(old, no_color=True)
    for line in capsys.readouterr().out.splitlines():
        if line.startswith("Status:"):
            assert line.strip() == f"Status:     assumed (replaced by E-{new})"
            return
    pytest.fail("no Status: line in `task show` output")


def test_task_show_agent_puts_the_note_on_the_status_line(
    seeded_project_at_cwd, capsys
):
    old, new = _superseded_pair()
    task_cmd.detail_item(old, agent=True)
    out = capsys.readouterr().out
    # key=value, in the status line's own position — not buried in `links=`.
    assert f"status=assumed replaced_by=E-{new}" in out


def test_task_show_json_emits_the_relation_ungated(seeded_project_at_cwd, capsys):
    # `unverified` is NOT terminal, so the human/--agent note is suppressed — but
    # --json is data, and the relation is emitted anyway.
    old, new = _superseded_pair(status="unverified")
    task_cmd.detail_item(old, as_json=True)
    payload = json.loads(capsys.readouterr().out)
    assert payload["replaced_by"] == [f"E-{new}"]


def test_task_show_json_always_carries_the_key(seeded_project_at_cwd, capsys):
    tid = _add_task("Add an unreplaced thing", status="assumed")
    task_cmd.detail_item(tid, as_json=True)
    assert json.loads(capsys.readouterr().out)["replaced_by"] == []


def test_task_list_renders_the_bare_status(seeded_project_at_cwd, capsys):
    """E-2064 reversed E-1956 here: the table's Status column is shared by every
    row, so one annotated cell was charging every title for a fact that is one
    `task show` away. The row still appears — only the note is gone."""
    old, new = _superseded_pair()
    task_cmd.show_plan(show_all=True)
    out = capsys.readouterr().out
    assert f"E-{old}" in out
    assert "replaced by" not in out


def test_task_list_default_view_is_unchanged(seeded_project_at_cwd, capsys):
    # The default listing excludes terminal statuses outright, so no row can
    # carry a note and the Status column keeps its original width.
    _superseded_pair()
    _add_task("Add an open thing", status="ready")
    task_cmd.show_plan()
    out = capsys.readouterr().out
    assert "replaced by" not in out


def test_task_list_agent_puts_the_note_after_the_status(seeded_project_at_cwd, capsys):
    old, new = _superseded_pair()
    task_cmd.show_plan(show_all=True, agent=True)
    out = capsys.readouterr().out
    assert f"E-{old} now assumed replaced_by=E-{new} " in out


def test_task_list_json_emits_the_relation_ungated(seeded_project_at_cwd, capsys):
    old, new = _superseded_pair(status="underway")
    task_cmd.show_plan(show_all=True, as_json=True)
    rows = {r["id"]: r for r in json.loads(capsys.readouterr().out)["rows"]}
    assert rows[f"E-{old}"]["replaced_by"] == [f"E-{new}"]
    assert rows[f"E-{new}"]["replaced_by"] == []


# ─── obsolete and the replacement axis ───────────────────────────────────────
#
# `obsolete` means "no longer needed, and nothing replaced it" — the same line
# Endless already draws for decisions. It is keyed on whether anything took the
# work over, NEVER on whether the work shipped. The gate that refused it on
# shipped work is gone: code being DELETED rather than superseded has no
# successor for `task replace` to name, and `declined` (an active decision not
# to DO the work) is false of work that was built and landed.


@pytest.mark.parametrize(
    "shipped", ["unverified", "confirmed", "assumed", "completed"]
)
def test_update_to_obsolete_is_allowed_on_shipped_work(
    seeded_project_at_cwd, shipped
):
    tid = _add_task("Add a shipped thing", status=shipped)
    task_cmd.update_plan(tid, status="obsolete")  # must not raise
    assert _status(tid) == "obsolete"


@pytest.mark.parametrize(
    "open_status",
    ["untriaged", "unplanned", "submitted", "ready", "underway", "revisit"],
)
def test_update_to_obsolete_is_allowed_on_unshipped_work(
    seeded_project_at_cwd, open_status
):
    # `revisit` is in this list deliberately: work that shipped and was then
    # reopened is genuinely back in play, and re-closing it is a real call.
    tid = _add_task("Add an unshipped thing", status=open_status)
    task_cmd.update_plan(tid, status="obsolete")  # must not raise


def test_other_transitions_on_shipped_work_are_untouched(seeded_project_at_cwd):
    tid = _add_task("Add a shipped thing", status="assumed")
    task_cmd.update_plan(tid, status="declined", outcome="not worth keeping")
    assert _status(tid) == "declined"


def test_replace_allows_an_explicit_obsolete_on_shipped_work(
    seeded_project_at_cwd
):
    # The DEFAULT still holds a shipped status (next test) — that is about not
    # overwriting which terminal the work reached. An explicit --status is the
    # caller overriding that, and it is no longer refused.
    old = _add_task("Add a shipped thing", status="assumed")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new, status="obsolete")
    assert _status(old) == "obsolete"
    assert task_cmd.replaced_by_map([old]) == {old: [new]}


def test_replace_holds_a_shipped_status_by_default(seeded_project_at_cwd):
    old = _add_task("Add a shipped thing", status="assumed")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new)
    assert _status(old) == "assumed"
    assert task_cmd.replaced_by_map([old]) == {old: [new]}


def test_replace_defaults_to_superseded_on_unshipped_work(
    seeded_project_at_cwd
):
    old = _add_task("Add a stale idea", status="unplanned")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new)
    assert _status(old) == "superseded"


def test_superseded_is_refused_without_an_actual_replacement(
    seeded_project_at_cwd
):
    """`superseded` asserts a fact about ANOTHER row. Set by hand with no
    relation it names a successor that does not exist."""
    tid = _add_task("Add a thing nothing replaced", status="ready")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="superseded")
    msg = str(exc.value.message)
    assert "nothing replaced it" in msg
    assert "task replace" in msg       # names the command that records both
    assert "obsolete" in msg           # names the status that IS true of it
    assert _status(tid) == "ready"     # and nothing was written


def test_superseded_is_allowed_once_the_relation_exists(
    seeded_project_at_cwd
):
    old = _add_task("Add a stale idea", status="ready")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new, status="ready")   # relation only
    task_cmd.update_plan(old, status="superseded")    # must not raise
    assert _status(old) == "superseded"


def test_replace_persists_an_outcome_when_the_status_is_held(
    seeded_project_at_cwd
):
    # The held path skips task.status_changed (it would record a no-op
    # transition), so the outcome has to reach the row some other way.
    old = _add_task("Add a shipped thing", status="confirmed")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new, outcome="superseded by the rebuild")
    row = db.query("SELECT status, outcome FROM tasks WHERE id = ?", (old,))[0]
    assert row["status"] == "confirmed"
    assert row["outcome"] == "superseded by the rebuild"


def _recorded_event_kinds(monkeypatch) -> list[str]:
    """Capture the event kinds replace_task emits, in order."""
    from endless import event_bridge
    kinds: list[str] = []

    def _fake(kind, **_kwargs):
        kinds.append(kind)

    monkeypatch.setattr(event_bridge, "emit_event", _fake)
    return kinds


def test_replace_emits_no_status_event_when_the_status_is_held(
    seeded_project_at_cwd, monkeypatch
):
    kinds = _recorded_event_kinds(monkeypatch)
    old = _add_task("Add a shipped thing", status="assumed")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new)
    # A status_changed whose old and new are the same value would write a no-op
    # transition into the ledger and misreport the replace as a status change.
    assert "task.status_changed" not in kinds


def test_replace_routes_a_held_outcome_through_fields_updated(
    seeded_project_at_cwd, monkeypatch
):
    kinds = _recorded_event_kinds(monkeypatch)
    old = _add_task("Add a shipped thing", status="assumed")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new, outcome="superseded by the rebuild")
    assert "task.status_changed" not in kinds
    assert "task.fields_updated" in kinds


def test_replace_still_emits_a_status_event_when_the_status_moves(
    seeded_project_at_cwd, monkeypatch
):
    kinds = _recorded_event_kinds(monkeypatch)
    old = _add_task("Add a stale idea", status="unplanned")
    new = _add_task("Add the replacement", status="underway")
    task_cmd.replace_task(old, new)
    assert "task.status_changed" in kinds


# ─── one status vocabulary (the --help defect's root cause) ──────────────────


def test_help_list_and_validator_read_the_same_vocabulary():
    assert cli.TASK_STATUSES is statuses.TASK_STATUSES
    for status in statuses.TASK_STATUSES:
        assert status in statuses.TASK_STATUS_HELP


def test_vocabulary_carries_the_two_statuses_the_help_had_dropped():
    assert "submitted" in statuses.TASK_STATUSES
    assert "completed" in statuses.TASK_STATUSES


def test_update_accepts_every_status_the_help_advertises(seeded_project_at_cwd):
    # The defect in reverse: `submitted` was advertised nowhere AND rejected by
    # update_plan. Advertising and accepting are now the same list.
    #
    # 'Audit' as the lead verb because `completed` carries a separate, unrelated
    # gate (E-1240: the title's lead verb must be marked completable) — this test
    # is about the status vocabulary, and tripping that gate would only prove
    # the other one still works.
    for status in statuses.TASK_STATUSES:
        tid = _add_task(f"Audit a thing for {status}", status="underway")
        try:
            task_cmd.update_plan(tid, status=status, outcome="because", force=True)
        except click.ClickException as exc:
            # A status may still be refused for a reason that NAMES itself —
            # since E-2018, an illegal lifecycle edge out of `underway` is one.
            # What must never happen is a status the --help advertises being
            # refused as UNKNOWN, which is the defect this test covers.
            assert "Invalid status" not in exc.message, status
        else:
            assert _status(tid) == status


def test_update_still_rejects_an_unknown_status(seeded_project_at_cwd):
    tid = _add_task("Add a thing", status="underway")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="nonsense")
    assert "Invalid status" in str(exc.value.message)
