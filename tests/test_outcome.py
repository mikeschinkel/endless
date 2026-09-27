"""Tests for the outcome and reason content and the abandonment verbs (E-787,
E-2175, E-1531).

E-1531 split `tasks.outcome` in two: `outcome` is the deliverable, `reason` is
why a task ended. Every abandonment route stores its text as `reason`."""

import ast
import json
from pathlib import Path

import click
import pytest
from click.testing import CliRunner

from endless import db, task_cmd
from endless.cli import main


def _add_task(title: str, status: str = "ready") -> int:
    cur = db.execute(
        "INSERT INTO tasks (project_id, title, status, type_id, phase, created_at) "
        "VALUES (1, ?, ?, 1, 'now', datetime('now'))",
        (title, status),
    )
    return cur.lastrowid


def _set_content(task_id: int, **content: str) -> None:
    """Seed task_content rows directly (E-1531: no longer tasks columns)."""
    for name, value in content.items():
        db.execute(
            "INSERT INTO task_content (task_id, name, content) VALUES (?, ?, ?) "
            "ON CONFLICT(task_id, name) DO UPDATE SET content = excluded.content",
            (task_id, name, value),
        )


def _status_outcome(task_id: int) -> tuple[str, str | None]:
    row = db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))
    return row[0]["status"], db.task_content(task_id).get("outcome")


def _status_reason(task_id: int) -> tuple[str, str | None]:
    row = db.query("SELECT status FROM tasks WHERE id = ?", (task_id,))
    return row[0]["status"], db.task_content(task_id).get("reason")


# ─── decline ──────────────────────────────────────────────────────────────────


def test_task_decline_writes_reason(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="wrong premise")
    status, reason = _status_reason(tid)
    assert status == "declined"
    assert reason == "wrong premise"
    assert _status_outcome(tid)[1] is None


def test_task_decline_requires_reason(seeded_project_at_cwd):
    tid = _add_task("Sample")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "decline", f"E-{tid}"])
    assert result.exit_code != 0
    assert "--reason" in result.output or "Missing option" in result.output


def test_task_decline_blank_reason_rejected(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.decline_item(tid, reason="   ")
    assert "reason is required" in str(exc.value.message).lower()


# ─── confirm ──────────────────────────────────────────────────────────────────


def test_task_confirm_with_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.complete_item(tid, outcome="all green")
    status, outcome = _status_outcome(tid)
    assert status == "confirmed"
    assert outcome == "all green"


def test_task_confirm_without_outcome_still_works(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.complete_item(tid)
    status, outcome = _status_outcome(tid)
    assert status == "confirmed"
    assert outcome is None


# ─── assume ───────────────────────────────────────────────────────────────────


def test_task_assume_with_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.assume_item(tid, outcome="believed working")
    status, outcome = _status_outcome(tid)
    assert status == "assumed"
    assert outcome == "believed working"


# ─── replace ──────────────────────────────────────────────────────────────────


def test_task_replace_default_superseded(seeded_project_at_cwd):
    # E-2144: the unshipped default is `superseded`, not `obsolete` — the whole
    # point of this call is recording that something replaced it, and
    # `obsolete` means nothing did.
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new, outcome="folded into the replacement")
    status, reason = _status_reason(old)
    assert status == "superseded"
    assert reason == "folded into the replacement"


def test_task_replace_with_status_declined_requires_outcome(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.replace_task(old, new, status="declined")
    assert "reason is required" in str(exc.value.message).lower()


def test_task_replace_with_status_declined_and_outcome(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new, status="declined", outcome="superseded by E-NEW")
    status, reason = _status_reason(old)
    assert status == "declined"
    assert reason == "superseded by E-NEW"


# ─── update ───────────────────────────────────────────────────────────────────


def test_task_update_status_declined_requires_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="declined")
    assert "reason is required" in str(exc.value.message).lower()


def test_task_update_outcome_standalone(seeded_project_at_cwd):
    tid = _add_task("Sample", status="underway")
    task_cmd.update_plan(tid, outcome="initial note")
    status, outcome = _status_outcome(tid)
    assert status == "underway"  # unchanged
    assert outcome == "initial note"


def test_task_update_reason_amends(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="first reason")
    task_cmd.update_plan(tid, reason="amended reason")
    status, reason = _status_reason(tid)
    assert status == "declined"
    assert reason == "amended reason"


def test_outcome_without_a_transition_stays_an_outcome(seeded_project_at_cwd):
    """Only text given WITH an abandonment status is a reason. `--outcome` on
    its own edits the deliverable, whatever status the task sits at."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="the reason")
    task_cmd.update_plan(tid, outcome="a deliverable after all")
    assert _status_reason(tid) == ("declined", "the reason")
    assert _status_outcome(tid) == ("declined", "a deliverable after all")


# ─── abandonment requires a reason (E-2175) ───────────────────────────────────
#
# A task can end three ways without having shipped, and until E-2175 only
# `declined` was refused without a reason (ED-1022). `obsolete` recorded
# nothing at all; `superseded` recorded a successor, which says WHAT took the
# work over and not WHY it was handed on. These assert the widened guard on
# every route to all three.


def test_task_update_status_obsolete_requires_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="obsolete")
    msg = str(exc.value.message)
    assert "reason is required" in msg.lower()
    # The refusal must name the flag that satisfies it, not just complain.
    assert "--reason" in msg


def test_task_update_status_obsolete_blank_outcome_rejected(seeded_project_at_cwd):
    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="obsolete", outcome="   ")
    assert "reason is required" in str(exc.value.message).lower()


def test_task_update_status_obsolete_with_outcome_stores_it_as_reason(seeded_project_at_cwd):
    """`--outcome` with an abandonment status is that status's reason — the
    flag every route spelled it with before E-1531 — so it lands as `reason`."""
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, status="obsolete", outcome="the API it wrapped is gone")
    assert _status_reason(tid) == ("obsolete", "the API it wrapped is gone")
    assert _status_outcome(tid)[1] is None


def test_task_update_status_obsolete_with_reason(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, status="obsolete", reason="the API it wrapped is gone")
    assert _status_reason(tid) == ("obsolete", "the API it wrapped is gone")


def test_a_stored_reason_does_not_satisfy_the_guard(seeded_project_at_cwd):
    """Every closing move owes its own why (E-1531, Mike's ruling): a reason
    already stored on the task is not accepted in place of one given now."""
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, reason="written before the decision was taken")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="obsolete")
    assert "reason is required" in str(exc.value.message).lower()


def test_a_stored_reason_does_not_satisfy_replace(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.update_plan(old, reason="stored earlier")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.replace_task(old, new)
    assert "reason is required" in str(exc.value.message).lower()


def test_a_stored_outcome_does_not_satisfy_the_guard(seeded_project_at_cwd):
    """The other half: a deliverable is not a reason, which is the whole
    point of keeping them apart."""
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, outcome="some findings")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="obsolete")
    assert "reason is required" in str(exc.value.message).lower()


def test_abandoning_findings_work_keeps_the_findings(seeded_project_at_cwd):
    """The concrete cost the split removes: abandoning a research task used to
    overwrite its findings with why it was abandoned."""
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, outcome="the findings")
    task_cmd.update_plan(tid, status="obsolete", outcome="nobody needs them now")
    assert _status_outcome(tid) == ("obsolete", "the findings")
    assert _status_reason(tid) == ("obsolete", "nobody needs them now")


def test_epic_update_status_obsolete_requires_outcome(seeded_project_at_cwd):
    """`epic update` is a second front door onto update_plan, so it inherits the
    guard rather than needing one of its own."""
    from endless import epic_cmd

    tid = _add_task("Sample")
    with pytest.raises(click.ClickException) as exc:
        epic_cmd.update_epic(tid, status="obsolete")
    assert "reason is required" in str(exc.value.message).lower()


def test_task_replace_with_status_obsolete_requires_outcome(seeded_project_at_cwd):
    """The `replaced_by` relation records WHAT replaced a task, not WHY it went
    away; an explicit --status obsolete still owes the reason."""
    old = _add_task("Old")
    new = _add_task("New")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.replace_task(old, new, status="obsolete")
    assert "reason is required" in str(exc.value.message).lower()


def test_task_replace_with_status_obsolete_and_outcome(seeded_project_at_cwd):
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new, status="obsolete", outcome="deleted outright")
    assert _status_reason(old) == ("obsolete", "deleted outright")


@pytest.mark.parametrize("shipped", ["unverified", "confirmed", "assumed", "completed"])
def test_task_replace_on_shipped_work_needs_no_outcome(seeded_project_at_cwd, shipped):
    """The one exemption, and it is structural rather than a carve-out: work
    that shipped keeps the terminal it earned, was never abandoned, and so
    reaches none of the three statuses the guard covers."""
    old = _add_task("Old", status=shipped)
    new = _add_task("New")
    task_cmd.replace_task(old, new)
    status, outcome = _status_outcome(old)
    assert status == shipped
    assert outcome is None


def test_task_replace_default_superseded_requires_outcome(seeded_project_at_cwd):
    """The plainest form of the commonest abandonment. The `replaced_by`
    relation names the successor; it does not say why the work was handed on,
    and leaving this route unguarded is the side door that would make the rule
    unenforceable everywhere else."""
    old = _add_task("Old")
    new = _add_task("New")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.replace_task(old, new)
    msg = str(exc.value.message)
    assert "reason is required" in msg.lower()
    assert "--outcome" in msg
    # Refused before anything was written: no relation, no status change.
    assert task_cmd.replaced_by_map([old]) == {}
    assert _status_outcome(old) == ("ready", None)


def test_task_update_status_superseded_requires_outcome(seeded_project_at_cwd):
    """The other route to the status, once the relation already exists."""
    old = _add_task("Old")
    new = _add_task("New")
    task_cmd.replace_task(old, new, status="ready")   # relation only
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(old, status="superseded")
    assert "reason is required" in str(exc.value.message).lower()


def test_superseded_with_no_relation_is_refused_for_the_relation_first(
    seeded_project_at_cwd
):
    """Both facts missing: the refusal worth printing is the one that says the
    status is wrong for this row at all, not the one that teaches a flag for a
    status the caller is about to be told not to use."""
    tid = _add_task("Nothing replaced this")
    with pytest.raises(click.ClickException) as exc:
        task_cmd.update_plan(tid, status="superseded")
    msg = str(exc.value.message)
    assert "nothing replaced it" in msg
    assert "reason is required" not in msg.lower()


def test_existing_reasonless_obsolete_rows_still_read(seeded_project_at_cwd):
    """The guard is on the TRANSITION, not on the row: the rows that predate it
    are deliberately not backfilled, and must still read, render and query."""
    tid = _add_task("Legacy", status="obsolete")
    runner = CliRunner()

    shown = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert shown.exit_code == 0, shown.output
    assert "obsolete" in shown.output

    listed = runner.invoke(main, ["task", "list", "--status", "obsolete"])
    assert listed.exit_code == 0, listed.output
    assert f"E-{tid}" in listed.output

    as_json = runner.invoke(
        main, ["task", "show", f"E-{tid}", "--outcome", "--json"]
    )
    assert as_json.exit_code == 0, as_json.output
    payload = json.loads(as_json.output)
    assert payload["status"] == "obsolete"
    assert not payload.get("outcome")


def test_the_guard_fires_for_exactly_the_unshipped_terminals():
    """The rule is "every status that ends a task without it having shipped",
    and these three are that set. A status added to the vocabulary without a
    decision about which side of that line it falls on fails here."""
    from endless.statuses import TASK_STATUSES

    fired = set()
    for status in TASK_STATUSES:
        try:
            task_cmd._require_reason_for_abandonment(status, None)
        except click.ClickException:
            fired.add(status)
    assert fired == {"declined", "obsolete", "superseded"}
    assert task_cmd._require_reason_for_abandonment(None, None) is None


# ─── every route to an abandonment status is guarded ──────────────────────────
#
# Single-site enforcement is the failure E-2175 exists to prevent: `declined`
# has had no reasonless row since ED-1022 covered all three of its call sites,
# while `obsolete` leaked from wherever it was not covered. These enumerate the
# routes structurally, so the next one added is a test failure rather than a
# hole someone notices in three months.

_GUARD = "_require_reason_for_abandonment"
_ABANDONMENT = {"declined", "obsolete"}
_SRC = Path(__file__).resolve().parents[1] / "src" / "endless"


def _parse(name):
    return ast.parse((_SRC / name).read_text())


def _functions(tree):
    return [n for n in ast.walk(tree)
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))]


def _resolve(fn, node):
    """Possible string values of `node` inside `fn`, or None if not knowable.

    Folds the one indirection the emitters actually use — a local assigned a
    string literal and then read back into the payload dict.
    """
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return {node.value}
    if not isinstance(node, ast.Name):
        return None
    values = set()
    for stmt in ast.walk(fn):
        if not isinstance(stmt, ast.Assign):
            continue
        if not any(isinstance(t, ast.Name) and t.id == node.id
                   for t in stmt.targets):
            continue
        if isinstance(stmt.value, ast.Constant):
            # `x = None` is the "nothing to emit" sentinel, not a status.
            if isinstance(stmt.value.value, str):
                values.add(stmt.value.value)
            elif stmt.value.value is not None:
                return None
        else:
            return None
    return values or None


def _emits_status_change(fn):
    for node in ast.walk(fn):
        if isinstance(node, ast.Call) and getattr(node.func, "id", None) == "emit_event":
            for kw in node.keywords:
                if (kw.arg == "kind" and isinstance(kw.value, ast.Constant)
                        and kw.value.value == "task.status_changed"):
                    return True
    return False


def _new_status_nodes(fn):
    out = []
    for node in ast.walk(fn):
        if not isinstance(node, ast.Dict):
            continue
        for key, value in zip(node.keys, node.values):
            if isinstance(key, ast.Constant) and key.value == "new_status":
                out.append(value)
    return out


def _calls_guard(fn):
    return any(isinstance(n, ast.Call) and getattr(n.func, "id", None) == _GUARD
               for n in ast.walk(fn))


def test_task_cmd_status_emitters_are_pinned_or_guarded():
    """In task_cmd, a function emitting the status-change event either pins
    `new_status` to literals that are not abandonment statuses, or takes it from
    the caller — and then it must call the guard."""
    tree = _parse("task_cmd.py")
    emitters = [fn for fn in _functions(tree) if _emits_status_change(fn)]
    assert len(emitters) >= 8, f"expected the status emitters, found {len(emitters)}"
    for fn in emitters:
        nodes = _new_status_nodes(fn)
        assert nodes, f"task_cmd.{fn.name} emits the event with no new_status"
        resolved = [_resolve(fn, n) for n in nodes]
        caller_supplied = any(r is None for r in resolved)
        pinned = set().union(*[r for r in resolved if r]) if any(resolved) else set()
        if caller_supplied or (_ABANDONMENT & pinned):
            assert _calls_guard(fn), (
                f"task_cmd.{fn.name} can set an abandonment status but never "
                f"calls {_GUARD} — that is the single-site leak E-2175 closed."
            )


def test_the_guard_is_called_from_exactly_the_expected_front_doors():
    """Named, so deleting a call is a failure rather than a silent hole."""
    callers = {fn.name for fn in _functions(_parse("task_cmd.py"))
               if _calls_guard(fn)}
    assert callers == {"update_plan", "replace_task", "decline_item"}, callers


def test_triage_can_never_reach_an_abandonment_status():
    """triage.apply takes its status from the model's verdict, so what bounds it
    is the vocabulary it parses against."""
    from endless import triage

    assert _ABANDONMENT.isdisjoint({d.lower() for d in triage._DECISIONS})


def test_session_cmd_only_ever_emits_pinned_non_abandonment_statuses():
    """session_cmd routes through one helper taking `new_status` as a parameter;
    what bounds it is the call sites, so those are what this reads."""
    tree = _parse("session_cmd.py")
    seen = []
    for fn in _functions(tree):
        for node in ast.walk(fn):
            if not isinstance(node, ast.Call):
                continue
            if getattr(node.func, "id", None) != "_emit_task_status_change":
                continue
            assert len(node.args) >= 4, (
                f"session_cmd.{fn.name} calls the helper without a positional "
                "new_status — this check can no longer see what it sets."
            )
            values = _resolve(fn, node.args[3])
            assert values is not None, (
                f"session_cmd.{fn.name} passes a new_status this check cannot "
                "resolve; pin it to a literal or route it through the guard."
            )
            assert _ABANDONMENT.isdisjoint(values), (fn.name, values)
            seen.extend(values)
    assert seen, "expected session_cmd to emit status changes"


# ─── show ─────────────────────────────────────────────────────────────────────


def test_task_show_outcome_flag_renders_outcome(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.update_plan(tid, outcome="some context")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--outcome"])
    assert result.exit_code == 0
    assert "some context" in result.output
    # E-1577: outcome renders as a "— Outcome —" section, not inline.
    assert "— Outcome —" in result.output
    # And the inline "Outcome:" label-value field is NOT used anymore.
    assert "Outcome:" not in result.output


def test_task_show_declined_hides_reason_by_default(seeded_project_at_cwd):
    """E-1601: content is flag-gated for every status, declined included. With
    no flag the snapshot shows a one-line char-count placeholder, not the
    reason. E-1531: a decline's text is its Reason, under its own heading."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="declined for testing")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Reason:" in result.output
    assert "(--reason to display)" in result.output
    assert "— Reason —" not in result.output
    assert "declined for testing" not in result.output
    assert "Outcome:" not in result.output


def test_task_show_declined_reason_flag_reveals_reason(seeded_project_at_cwd):
    """E-1601: --reason restores the full section for a declined task."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="declined for testing")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--reason"])
    assert result.exit_code == 0
    assert "— Reason —" in result.output
    assert "declined for testing" in result.output
    assert "(--reason to display)" not in result.output


def test_task_show_outcome_placeholder_char_count(seeded_project_at_cwd):
    """E-1601: the placeholder reports the exact len() of the outcome body."""
    tid = _add_task("Sample")
    body = "x" * 137
    task_cmd.update_plan(tid, outcome=body)
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    # Label column is padded, so match the count text independently of spacing.
    assert "Outcome:" in result.output
    assert "137 chars (--outcome to display)" in result.output
    assert body not in result.output


def test_task_show_completed_hides_outcome_by_default(seeded_project_at_cwd):
    """E-1601: the motivating case (E-1600) — a `completed` task's deliverable
    no longer floods the snapshot; the old status-keyed auto-display is gone."""
    tid = _add_task("Sample")
    db.execute("UPDATE tasks SET status = 'completed' WHERE id = ?", (tid,))
    _set_content(tid, outcome="a long research deliverable body")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Outcome:" in result.output
    assert "(--outcome to display)" in result.output
    assert "— Outcome —" not in result.output
    assert "a long research deliverable body" not in result.output


def test_task_show_plan_and_analysis_placeholders(seeded_project_at_cwd):
    """E-1601: plan and analysis also collapse to flag-named placeholders."""
    tid = _add_task("Sample")
    _set_content(tid, plan="body plan content", analysis="analysis design content")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    assert "Plan:" in result.output
    assert "(--plan to display)" in result.output
    assert "Analysis:" in result.output
    assert "(--analysis to display)" in result.output
    assert "body plan content" not in result.output
    assert "analysis design content" not in result.output


def test_task_show_placeholder_precedes_description(seeded_project_at_cwd):
    """E-1601: hidden-field placeholders are single-line `Label: value` fields,
    so they render with the header group ABOVE the multi-line Description, not
    interleaved with the sections below it."""
    tid = _add_task("Sample")
    db.execute("UPDATE tasks SET description = ? WHERE id = ?",
               ("a multi-line description body", tid))
    _set_content(tid, outcome="the outcome deliverable")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}"])
    assert result.exit_code == 0
    outcome_idx = result.output.find("Outcome:")
    desc_idx = result.output.find("— Description —")
    assert outcome_idx != -1
    assert desc_idx != -1
    assert outcome_idx < desc_idx, \
        "the Outcome placeholder must render before the Description section"


def test_task_show_full_section_follows_description(seeded_project_at_cwd):
    """E-1601: the full (flagged) body still renders as a section AFTER
    Description."""
    tid = _add_task("Sample")
    db.execute("UPDATE tasks SET description = ? WHERE id = ?",
               ("a multi-line description body", tid))
    _set_content(tid, outcome="the outcome deliverable")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--outcome"])
    assert result.exit_code == 0
    desc_idx = result.output.find("— Description —")
    outcome_idx = result.output.find("— Outcome —")
    assert desc_idx != -1
    assert outcome_idx != -1
    assert outcome_idx > desc_idx, \
        "the full Outcome section must render after the Description section"


def test_task_show_all_fields_reveals_everything(seeded_project_at_cwd):
    """E-1601: --all-fields shows every section with no placeholders left."""
    tid = _add_task("Sample")
    _set_content(tid, plan="body plan content", analysis="analysis design content",
                 outcome="outcome deliverable", reason="why it ended",
                 notes="some notes")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--all-fields"])
    assert result.exit_code == 0
    # One heading per content name, in the vocabulary's display order (E-1531).
    order = [result.output.find(f"— {h} —")
             for h in ("Analysis", "Plan", "Outcome", "Reason", "Notes")]
    assert -1 not in order, result.output
    assert order == sorted(order)
    assert "to display)" not in result.output


def test_task_show_outcome_section_renders_after_plan(seeded_project_at_cwd):
    """E-1577: the outcome section appears AFTER the plan section."""
    tid = _add_task("Sample")
    _set_content(tid, plan="body plan content", outcome="outcome content")
    runner = CliRunner()
    result = runner.invoke(main, ["task", "show", f"E-{tid}", "--plan", "--outcome"])
    assert result.exit_code == 0
    plan_idx = result.output.find("— Plan —")
    outcome_idx = result.output.find("— Outcome —")
    assert plan_idx != -1
    assert outcome_idx != -1
    assert outcome_idx > plan_idx, "outcome section must follow plan section"


def test_task_show_agent_reason_gated(seeded_project_at_cwd):
    """E-1601: --agent collapses content to a char marker by default; --reason
    pulls the body as a `## Reason` section."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="agent-mode reason")
    runner = CliRunner()
    default = runner.invoke(main, ["task", "show", f"E-{tid}", "--agent"])
    assert default.exit_code == 0
    assert f"reason_chars={len('agent-mode reason')}" in default.output
    assert "## Reason" not in default.output
    revealed = runner.invoke(main, ["task", "show", f"E-{tid}", "--agent", "--reason"])
    assert revealed.exit_code == 0
    assert "## Reason" in revealed.output
    assert "agent-mode reason" in revealed.output


def test_task_show_json_outcome_ungated(seeded_project_at_cwd):
    """E-2126 inverts E-1601 for the machine format only: --json carries the
    outcome body with no flag, and outcome_chars still reports its true length.

    E-1601 applied the placeholder treatment to --json too, so this same call
    returned `"outcome": null` for a populated field. The gating was the whole
    point then; it is the defect now. What survives unchanged is the second
    half of the original assertion — outcome_chars is always present — and the
    fact that --outcome makes no difference to the payload."""
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="json reason")
    runner = CliRunner()
    default = json.loads(
        runner.invoke(main, ["task", "show", f"E-{tid}", "--json"]).output
    )
    assert default["reason"] == "json reason"
    assert default["reason_chars"] == len("json reason")
    assert default["outcome"] is None and default["outcome_chars"] == 0
    revealed = json.loads(
        runner.invoke(main, ["task", "show", f"E-{tid}", "--json", "--reason"]).output
    )
    assert revealed == default, "a display flag must not change the JSON payload"


# ─── event log ────────────────────────────────────────────────────────────────


def test_event_log_records_reason(seeded_project_at_cwd):
    tid = _add_task("Sample")
    task_cmd.decline_item(tid, reason="event-log reason")
    events_dir = seeded_project_at_cwd / ".endless" / "db-ledger"
    files = list(events_dir.glob("db-entries-*.jsonl"))
    assert files, "no event log file written"
    found = False
    for f in files:
        for line in f.read_text().splitlines():
            evt = json.loads(line)
            if (evt.get("kind") == "task.status_changed"
                    and evt.get("entity", {}).get("id") == str(tid)):
                payload = evt["payload"]
                if (payload.get("new_status") == "declined"
                        and payload.get("reason") == "event-log reason"):
                    found = True
                    break
    assert found, "decline event with reason not found in event log"


# ─── ledger round-trip ────────────────────────────────────────────────────────
#
# test_rebuild_db_preserves_outcome lived here: it declined a task, ran
# `endless-go event rebuild-db --confirm`, and read the outcome back out of the
# real database. E-2062 refuses that flag deliberately — its copy-back destroys
# four tables it cannot restore — and the claim never needed it. The two halves
# it was making are covered where each one lives:
#
#   - the decline EMITS the reason:
#     test_event_log_records_reason, immediately above.
#   - replaying that event REPRODUCES the outcome:
#     internal/events/projector_test.go,
#     TestProjectToTempDB_StatusChangeCarriesOutcome.
#
# That pair asserts the round trip with no built binary, no database write, and
# no dependence on a command that is disabled.
