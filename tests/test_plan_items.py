"""E-2280: itemized plans and reconciliation — the Python half.

The rules themselves (origins, freezing, which question settles a discrepancy,
the gate) are enforced in Go and tested there (internal/events/plan_items_test.go,
internal/reconcilegate). What lives here is what this side owns: the two file
formats, turning an items file into operations, rendering, and the sandbox
check that keeps the items.toml mirror off the real main checkout.
"""

from pathlib import Path

import pytest
from click.exceptions import ClickException

from endless import config, plan_items


def _write(tmp_path: Path, name: str, body: str) -> str:
    p = tmp_path / name
    p.write_text(body)
    return str(p)


# --- labels ----------------------------------------------------------------

@pytest.mark.parametrize("raw", ["P3", "p3", "3", "E-2280.P3"])
def test_parse_label_accepts_every_spelling(raw):
    assert plan_items.parse_label(raw) == 3


@pytest.mark.parametrize("raw", ["P0", "Px", "", "P03", "-1"])
def test_parse_label_refuses_garbage(raw):
    with pytest.raises(ClickException):
        plan_items.parse_label(raw)


# --- the items file ----------------------------------------------------------

def test_items_file_reads_items_in_order(tmp_path):
    path = _write(tmp_path, "i.toml", '''
[[item]]
statement = "The widget renders"
verify = "go test ./widget"

[[item]]
id = "P4"
kind = "exclusion"
statement = "Does not touch the ledger"
''')
    got = plan_items.parse_items_file(path)
    assert got == [
        {"seq": None, "kind": "deliverable", "statement": "The widget renders",
         "verify": "go test ./widget", "detail": "", "post_land": False},
        {"seq": 4, "kind": "exclusion", "statement": "Does not touch the ledger",
         "verify": "", "detail": "", "post_land": False},
    ]


def test_items_file_reads_the_post_land_mark(tmp_path):
    """E-2444: post_land = true marks an item; it must be a TOML boolean."""
    path = _write(tmp_path, "i.toml",
                  '[[item]]\nstatement = "P9 recorded after install"\npost_land = true\n')
    assert plan_items.parse_items_file(path)[0]["post_land"] is True
    bad = _write(tmp_path, "b.toml",
                 '[[item]]\nstatement = "x"\npost_land = "yes"\n')
    with pytest.raises(ClickException):
        plan_items.parse_items_file(bad)


@pytest.mark.parametrize("body", [
    '[[item]]\nstatement = "x"\nowner = "me"\n',          # unknown key
    'title = "x"\n[[item]]\nstatement = "x"\n',            # unknown top-level key
    '[[item]]\nkind = "wish"\nstatement = "x"\n',          # unknown kind
    '[[item]]\nstatement = ""\n',                          # empty statement
    '[[item]]\nstatement = """a\nb"""\n',                  # two lines
    '[[item]]\nid = "P1"\nstatement = "a"\n[[item]]\nid = "P1"\nstatement = "b"\n',
    '[[item]]\nstatement = 3\n',                           # not a string
    '',                                                    # no items
    'not toml [',
])
def test_items_file_refuses_what_it_does_not_define(tmp_path, body):
    with pytest.raises(ClickException):
        plan_items.parse_items_file(_write(tmp_path, "i.toml", body))


def _current(monkeypatch, rows):
    """Stand in for the Go read: active rows, and every row with --all."""
    def items(task_id, all_items=False):
        return [r for r in rows if all_items or r["state"] == "active"]
    monkeypatch.setattr(plan_items, "items", items)


def _row(seq, statement, kind="deliverable", state="active", sort=None, verify=None):
    return {"seq": seq, "statement": statement, "kind": kind, "state": state,
            "sort_order": sort if sort is not None else seq, "verify": verify,
            "detail": None}


def test_items_file_diff_adds_edits_withdraws_and_orders(monkeypatch):
    _current(monkeypatch, [_row(1, "a"), _row(2, "b"), _row(3, "c", state="withdrawn")])
    wanted = [
        {"seq": 2, "kind": "deliverable", "statement": "b2", "verify": "", "detail": ""},
        {"seq": None, "kind": "exclusion", "statement": "new", "verify": "", "detail": ""},
    ]
    ops = plan_items.ops_for_items_file(9, wanted)
    assert {"op": "withdraw", "seq": 1} in ops
    assert {"op": "edit", "seq": 2, "statement": "b2"} in ops
    add = next(o for o in ops if o["op"] == "add")
    move = next(o for o in ops if o["op"] == "move")
    assert add["statement"] == "new" and add["kind"] == "exclusion"
    # The file's order is the display order, past every existing sort order.
    assert move["sort_order"] == 4 and add["sort_order"] == 5


def test_loading_the_unchanged_set_changes_nothing(monkeypatch):
    _current(monkeypatch, [_row(1, "a", verify="t"), _row(2, "b", kind="exclusion")])
    wanted = [
        {"seq": 1, "kind": "deliverable", "statement": "a", "verify": "t", "detail": ""},
        {"seq": 2, "kind": "exclusion", "statement": "b", "verify": "", "detail": ""},
    ]
    assert plan_items.ops_for_items_file(9, wanted) == []


def test_items_file_refuses_unknown_and_withdrawn_ids(monkeypatch):
    _current(monkeypatch, [_row(1, "a"), _row(2, "b", state="withdrawn")])
    for seq in (7, 2):
        with pytest.raises(ClickException):
            plan_items.ops_for_items_file(9, [
                {"seq": seq, "kind": "deliverable", "statement": "x", "verify": "", "detail": ""}])


# --- the reconciliation file -------------------------------------------------

def test_reconcile_file_reads_entries(tmp_path):
    path = _write(tmp_path, "r.toml", '''
[[entry]]
item = "P1"
verdict = "differently"
note = "Another route"
answer_holds = true
reason = "Nothing it covers changed"

[[entry]]
item = "P2"
verdict = "kept"
''')
    assert plan_items.parse_reconcile_file(path) == [
        {"item_seq": 1, "verdict": "differently", "note": "Another route",
         "answer_holds": True, "reask": False, "reason": "Nothing it covers changed"},
        {"item_seq": 2, "verdict": "kept", "note": "", "answer_holds": False,
         "reask": False, "reason": ""},
    ]


@pytest.mark.parametrize("body", [
    '[[entry]]\nitem = "P1"\nverdict = "kept"\nmood = "fine"\n',
    '[[entry]]\nverdict = "kept"\n',
    'summary = "x"\n',
])
def test_reconcile_file_refuses_what_it_does_not_define(tmp_path, body):
    with pytest.raises(ClickException):
        plan_items.parse_reconcile_file(_write(tmp_path, "r.toml", body))


# --- rendering ---------------------------------------------------------------

def test_render_shows_verdict_answer_and_kept_reason():
    out = plan_items.render_items([{
        "label": "P1", "kind": "deliverable", "statement": "It renders",
        "origin": "planned", "state": "active", "approved_at": "x",
        "approved_statement": "It renders", "approved_kind": "deliverable",
        "verify": "go test", "detail": None, "verdict": "differently",
        "note": "via a template", "question_id": 7, "question_status": "answered",
        "answer": "fine", "holds_reason": "nothing changed",
    }])
    assert "P1" in out and "verify: go test" in out
    assert "done differently: via a template" in out
    assert "EQ-7 answered: fine" in out and "kept: nothing changed" in out


def test_render_flags_grown_unapproved_and_changed_items():
    base = {"kind": "deliverable", "verify": None, "detail": None, "verdict": None}
    out = plan_items.render_items([
        {**base, "label": "P1", "statement": "a", "origin": "grown", "state": "active",
         "approved_at": None},
        {**base, "label": "P2", "statement": "b2", "origin": "planned", "state": "active",
         "approved_at": "x", "approved_statement": "b", "approved_kind": "deliverable"},
    ])
    assert "[grown, unapproved]" in out
    assert "[changed since approval]" in out


# --- the sandbox check the mirror depends on ----------------------------------

def test_an_in_tree_sandbox_is_a_sandbox(tmp_path, monkeypatch):
    """The check used to look under the cache, where sandboxes lived before
    ED-1554, so it called every in-tree sandbox real — and a sandbox run
    committed its mirrors onto the real main checkout.
    """
    wt = tmp_path / "proj" / ".endless" / "worktrees" / "e-5"
    wt.mkdir(parents=True)
    monkeypatch.setattr(config, "worktree_path", lambda: wt)
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.sandbox_config_dir(wt))
    assert config.db_context_is_sandbox()
    monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", tmp_path / "real" / "endless")
    assert not config.db_context_is_sandbox()
    monkeypatch.setattr(config, "worktree_path", lambda: None)
    assert not config.db_context_is_sandbox()


# --- the CLI surfaces, with the Go seams stubbed -------------------------------

from click.testing import CliRunner  # noqa: E402

from endless.cli import main  # noqa: E402


@pytest.fixture
def seams(monkeypatch):
    """Stub every Go read and event emit; record the emits."""
    emitted: list[dict] = []
    state = {"status": "underway", "unapproved": False, "gate": {"pass": True, "problems": []}}

    def fake_go(args):
        verb = args[0]
        if verb == "plan-items":
            return {"rows": [_row(1, "a"), _row(2, "b"), _row(3, "c")]}
        if verb == "plan-readiness":
            return {"status": state["status"], "unapproved": state["unapproved"],
                    "must_itemize": True, "active": 1, "type": "todo", "deliverables": 1}
        if verb == "fingerprint":
            return {"fingerprint": "fp1", "head": "abc"}
        if verb == "task-questions":
            return {"rows": []}
        if verb == "reconcile-gate":
            return state["gate"]
        raise AssertionError(f"unexpected read {args}")

    def fake_emit(**kw):
        emitted.append(kw)
        return {"added": ["P4"]} if kw["kind"] == "task.plan_items_changed" else {"seq": 1}

    monkeypatch.setattr(plan_items, "_go", fake_go)
    monkeypatch.setattr(plan_items, "emit_event", fake_emit)
    monkeypatch.setattr(plan_items, "_project", lambda task_id: "proj")
    monkeypatch.setattr(plan_items, "write_mirror", lambda task_id: None)
    monkeypatch.setattr(plan_items, "task_repo", lambda task_id: Path("/repo"))
    return emitted, state


def _ops(emitted):
    return [e["payload"]["ops"] for e in emitted if e["kind"] == "task.plan_items_changed"]


def test_plan_add_edit_withdraw_shape_their_ops(seams):
    emitted, _ = seams
    r = CliRunner()
    assert r.invoke(main, ["task", "plan", "add", "E-7", " New thing ", "--kind",
                           "exclusion", "--verify", "t"]).exit_code == 0
    assert r.invoke(main, ["task", "plan", "edit", "E-7", "P2", "--statement", "b2"]).exit_code == 0
    assert r.invoke(main, ["task", "plan", "withdraw", "E-7", "P1", "E-7.P3"]).exit_code == 0
    assert _ops(emitted) == [
        [{"op": "add", "kind": "exclusion", "statement": "New thing", "verify": "t"}],
        [{"op": "edit", "seq": 2, "statement": "b2"}],
        [{"op": "withdraw", "seq": 1}, {"op": "withdraw", "seq": 3}],
    ]


def test_plan_move_rewrites_the_display_order(seams):
    emitted, _ = seams
    result = CliRunner().invoke(main, ["task", "plan", "move", "E-7", "P3", "1"])
    assert result.exit_code == 0, result.output
    assert [(o["seq"], o["sort_order"]) for o in _ops(emitted)[0]] == [(3, 4), (1, 5), (2, 6)]


def test_an_item_change_on_a_ready_task_drops_its_approval(seams):
    emitted, state = seams
    state.update(status="ready", unapproved=True)
    result = CliRunner().invoke(main, ["task", "plan", "add", "E-7", "More"])
    assert result.exit_code == 0, result.output
    change = [e for e in emitted if e["kind"] == "task.status_changed"]
    assert change and change[0]["payload"]["new_status"] == "submitted"


def test_an_item_change_underway_keeps_the_status(seams):
    emitted, state = seams
    state.update(status="underway", unapproved=True)
    CliRunner().invoke(main, ["task", "plan", "add", "E-7", "More"])
    assert not [e for e in emitted if e["kind"] == "task.status_changed"]


def test_reconcile_records_entries_with_the_fingerprint(seams, tmp_path):
    emitted, _ = seams
    path = _write(tmp_path, "r.toml", '[[entry]]\nitem = "P1"\nverdict = "as_planned"\n')
    result = CliRunner().invoke(main, ["task", "reconcile", "E-7", "--file", path])
    assert result.exit_code == 0, result.output
    evt = next(e for e in emitted if e["kind"] == "task.reconciled")
    assert evt["payload"]["fingerprint"] == "fp1" and evt["payload"]["head"] == "abc"
    assert evt["payload"]["entries"][0]["item_seq"] == 1
    assert "No discrepancies are waiting" in result.output


def test_the_gate_refusal_lists_every_problem(seams):
    _, state = seams
    state["gate"] = {"pass": False, "problems": ["the code changed", "EQ-3 is open"]}
    with pytest.raises(ClickException) as exc:
        plan_items.require_gate(7, "move to unverified")
    assert "the code changed" in str(exc.value.message) and "EQ-3 is open" in str(exc.value.message)


def test_the_plan_redirect_is_gone():
    result = CliRunner().invoke(main, ["plan", "list"])
    assert result.exit_code != 0
    assert "renamed to 'task'" not in result.output


# --- `task update --status ready` is approval by another verb ---------------

def _submitted_todo() -> int:
    from endless import task_cmd
    tid = task_cmd.add_item(title="Add a thing", description="short", plan="# plan\n")
    task_cmd.update_plan(item_id=tid, status="submitted", complexity="low", risk="low")
    return tid


def test_update_to_ready_refuses_a_todo_without_items(seeded_project_at_cwd):
    from endless import task_cmd
    tid = _submitted_todo()
    with pytest.raises(ClickException, match="no active deliverable plan item"):
        task_cmd.update_plan(item_id=tid, status="ready")


def test_update_to_ready_needs_every_item_approved(seeded_project_at_cwd):
    from endless import task_cmd
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid)
    with pytest.raises(ClickException, match="P1 are not approved as they stand"):
        task_cmd.update_plan(item_id=tid, status="ready")
    plan_items.approve(tid, ("P1",))
    task_cmd.update_plan(item_id=tid, status="ready")
    assert _status(tid) == "ready"


# --- items are approved one at a time by default ------------------------------

def _status(tid: int) -> str:
    from endless import db
    return db.query("SELECT status FROM tasks WHERE id = ?", (tid,))[0]["status"]


def test_approve_refuses_unapproved_items_and_names_the_route(seeded_project_at_cwd):
    from endless import task_cmd
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid, "One")
    itemize(tid, "Two")
    plan_items.approve(tid, ("P1",))
    with pytest.raises(ClickException) as exc:
        task_cmd.approve_item(tid)
    msg = str(exc.value)
    assert "plan items P2 are not approved" in msg
    assert f"endless task plan approve E-{tid} P2" in msg
    assert "--include-plan-items" in msg
    assert _status(tid) == "submitted"


def test_approving_each_item_then_the_task(seeded_project_at_cwd, capsys):
    from endless import task_cmd
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid, "One")
    itemize(tid, "Two")
    plan_items.approve(tid, ("P1",))
    assert "Still to approve: P2" in capsys.readouterr().out
    plan_items.approve(tid, ("P2",))
    assert "Every plan item" in capsys.readouterr().out
    task_cmd.approve_item(tid)
    assert _status(tid) == "ready"


def test_include_plan_items_approves_them_all(seeded_project_at_cwd):
    from endless import task_cmd
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid, "One")
    itemize(tid, "Two")
    task_cmd.approve_item(tid, include_plan_items=True)
    assert _status(tid) == "ready"
    assert plan_items.unapproved(tid) == []


def test_approving_a_withdrawn_item_accepts_its_withdrawal(seeded_project_at_cwd):
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid, "One")
    itemize(tid, "Two")
    plan_items.approve(tid, ("P1", "P2"))
    plan_items.emit_ops(tid, [{"op": "withdraw", "seq": 2}], quiet=True, keep_status=True)
    assert [it["seq"] for it in plan_items.unapproved(tid)] == [2]
    plan_items.approve(tid, ("P2",))
    assert plan_items.unapproved(tid) == []


def test_approving_an_unknown_item_is_refused(seeded_project_at_cwd):
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid)
    with pytest.raises(ClickException, match="no plan item P9"):
        plan_items.approve(tid, ("P9",))


def test_spawn_refuses_items_the_user_has_not_approved(seeded_project_at_cwd):
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid)
    with pytest.raises(ClickException) as exc:
        plan_items.require_approved(tid, "spawned")
    assert "have not been approved" in str(exc.value)
    assert f"endless task plan approve E-{tid} P1" in str(exc.value)
    plan_items.baseline(tid)
    plan_items.require_approved(tid, "spawned")


def test_spawn_refuses_items_changed_since_approval(seeded_project_at_cwd):
    from plan_item_steps import itemize
    tid = _submitted_todo()
    itemize(tid)
    plan_items.baseline(tid)
    itemize(tid, "And a second thing")
    with pytest.raises(ClickException):
        plan_items.require_approved(tid, "spawned")


def test_a_task_with_no_items_needs_no_approval_to_spawn(seeded_project_at_cwd):
    plan_items.require_approved(_submitted_todo(), "spawned")


def test_hand_in_refuses_items_never_approved(seeded_project_at_cwd):
    """E-2485: items added while underway and never approved refuse the hand-in,
    naming the approve command, while the task is still where it can comply."""
    from endless import task_cmd
    from plan_item_steps import itemize, reconcile_as_planned
    tid = _submitted_todo()
    task_cmd.update_plan(item_id=tid, status="underway")
    itemize(tid)
    reconcile_as_planned(tid)
    with pytest.raises(ClickException) as exc:
        task_cmd.update_plan(item_id=tid, status="unverified")
    assert f"endless task plan approve E-{tid} P1" in str(exc.value)
    assert _status(tid) == "underway"
    plan_items.approve(tid, ("P1",))
    reconcile_as_planned(tid)
    task_cmd.update_plan(item_id=tid, status="unverified")
    assert _status(tid) == "unverified"
