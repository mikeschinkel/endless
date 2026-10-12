"""Itemized plans and their reconciliation — `endless task plan`,
`endless task reconcile`, and the gate before a verify (E-2280).

A plan item is a one-line statement of something that will be true when the
task is done, numbered `P3` within its task (`E-2280.P3` from outside it). A
`deliverable` states what the work makes true; an `exclusion` states what the
task will not do or change. After implementing and before setting the task to
`unverified`, the agent reconciles: one verdict per active item. Every
discrepancy becomes an open question tagged with its kind, and the gate refuses
`unverified` (and `assumed`) until the reconciliation is current and every one
of them is settled.

**Zero Python SQLite.** Reads go through `endless-go session-query`, writes
through `endless-go event emit`, which validates and completes each change under
the write lock (internal/events/plan_items.go). Nothing here re-implements a
rule the executor enforces; this module parses files, shapes payloads and
renders.
"""

import json
import tomllib
from pathlib import Path

import click

from endless import agent_help, config, doc_mirror, provenance
from endless.event_bridge import emit_event

KINDS = ("deliverable", "exclusion")
DELIVERABLE_VERDICTS = ("as_planned", "differently", "not_done", "blocked")
EXCLUSION_VERDICTS = ("kept", "not_kept")

# Keys an `--items-file` [[item]] table may carry. Anything else is refused:
# a format that silently drops an unknown key turns a typo into lost scope.
ITEM_KEYS = {"id", "kind", "statement", "verify", "detail"}
# Keys a reconciliation file [[entry]] table may carry.
ENTRY_KEYS = {"item", "verdict", "note", "answer_holds", "reask", "reason"}


def label(seq: int) -> str:
    return f"P{seq}"


def parse_label(s: str) -> int:
    """`P3`, `p3`, `3` or `E-2280.P3` → 3."""
    t = str(s).strip().rsplit(".", 1)[-1].lstrip("Pp")
    if not t.isdigit() or int(t) <= 0 or str(int(t)) != t:
        raise agent_help.no_report(
            f"Not a plan item number: {s!r}.",
            "Name the item as P<n>, the number `endless task plan list` shows")
    return int(t)


# Reads ---------------------------------------------------------------------

def _go(args: list[str]):
    from endless.question_cmd import _go as go
    return go(args)


def items(task_id: int, all_items: bool = False) -> list[dict]:
    args = ["plan-items", "--id", str(task_id)]
    if all_items:
        args.append("--all")
    return provenance.rows_of(_go(args))


def readiness(task_id: int) -> dict:
    return _go(["plan-readiness", "--id", str(task_id)])


def _project(task_id: int) -> str:
    from endless.question_cmd import _target
    return _target(task_id=task_id)["project"]


# Writes --------------------------------------------------------------------

def emit_ops(task_id: int, ops: list[dict], *, quiet: bool = False,
             keep_status: bool = False) -> dict:
    """Apply one batch of item operations, refresh the mirror, and drop a
    `ready` task's approval when the item set no longer matches it.
    """
    out = emit_event(
        kind="task.plan_items_changed",
        project=_project(task_id),
        entity_type="task",
        entity_id=str(task_id),
        payload={"ops": ops},
    ) or {}
    write_mirror(task_id)
    if not quiet:
        added = out.get("added") or []
        n = len(ops)
        click.echo(
            click.style("•", fg="cyan")
            + f" Plan items of E-{task_id}: {n} change{'s' if n != 1 else ''}"
            + (f" (added {', '.join(added)})" if added else "")
        )
    if not keep_status:
        unapprove_if_material(task_id)
    return out


def unapprove_if_material(task_id: int) -> bool:
    """A `ready` task whose item set differs from its approved baseline goes back
    to `submitted`: an item added, withdrawn, or with a changed statement or kind.

    Only from `ready`. Earlier statuses have no approval to lose; from `underway`
    on, a change is held by the gate until the user re-approves instead (Mike's
    choice on E-2280), so the work is never yanked out from under a session.
    """
    r = readiness(task_id)
    if r["status"] != "ready" or not r["unapproved"]:
        return False
    emit_event(
        kind="task.status_changed",
        project=_project(task_id),
        entity_type="task",
        entity_id=str(task_id),
        payload={"old_status": "ready", "new_status": "submitted", "cascade": False},
    )
    # Human-only (E-2120): an agent is shown the fields it asked to change, not
    # a status it did not ask for.
    if not agent_help.agent_facing():
        click.echo(
            f"E-{task_id} → submitted (its plan items changed, and approval was "
            "granted against the old ones). Approve it again with "
            f"`endless task approve E-{task_id}`.")
    return True


def baseline(task_id: int) -> None:
    """Copy the item set into its approved baseline, and close as moot every
    open discrepancy question on an item that is now withdrawn.
    """
    emit_event(
        kind="task.plan_baselined",
        project=_project(task_id),
        entity_type="task",
        entity_id=str(task_id),
        payload={},
    )
    from endless.question_cmd import resolve_questions
    for m in provenance.rows_of(_go(["moot-questions", "--id", str(task_id)])):
        resolve_questions(
            "withdraw", (m["id"],),
            f"moot: {label(m['item_seq'])} was withdrawn and the plan re-approved")


def write_mirror(task_id: int) -> Path | None:
    """Write `.endless/tasks/e-NNNN/items.toml` on the main checkout.

    Best-effort, like every mirror (see doc_mirror.write_to_main): the database
    is authoritative and the doc-mirrors sweep repairs a missed write. Nothing is
    written under a sandbox database, for the reason task_cmd._mirror_doc gives.
    """
    if config.db_context_is_sandbox():
        return None
    from endless.task_cmd import _main_root_for_task
    main_root = _main_root_for_task(task_id)
    if main_root is None:
        return None
    out = _go(["plan-items-toml", "--id", str(task_id)])
    content, rel = out["content"], out["path"]
    if not content:
        return None
    target = main_root / rel
    if target.exists() and target.read_text() == content:
        return None
    action = "update" if target.exists() else "add"
    return doc_mirror.write_to_main(
        main_root, rel, content,
        doc_mirror.task_doc_subject(action, "plan items", task_id),
        "plan items",
    )


# The items file ------------------------------------------------------------

def _load_toml(path: str, what: str) -> dict:
    try:
        return tomllib.loads(Path(path).read_text())
    except FileNotFoundError:
        raise agent_help.no_report(f"No such {what} file: {path}.",
                                   "Pass the path of an existing file and retry")
    except tomllib.TOMLDecodeError as e:
        raise agent_help.no_report(f"{path} is not valid TOML: {e}.",
                                   "Fix the file and retry; nothing was recorded")


def parse_items_file(path: str) -> list[dict]:
    """Read an `--items-file`: one `[[item]]` table per item, in display order.

        [[item]]
        id = "P3"              # optional: names an existing item to keep or edit
        kind = "deliverable"   # or "exclusion"; default deliverable
        statement = "One line: what will be true when the task is done."
        verify = "How to tell it is true."      # optional
        detail = "A short note."                # optional

    Refuses any key the format does not define.
    """
    data = _load_toml(path, "items")
    extra = set(data) - {"item"}
    if extra:
        raise agent_help.no_report(
            f"{path}: unknown top-level key(s) {', '.join(sorted(extra))}; "
            "an items file holds only [[item]] tables.",
            "Remove them and retry; nothing was recorded")
    raw = data.get("item", [])
    if not isinstance(raw, list) or not raw:
        raise agent_help.no_report(
            f"{path} has no [[item]] tables.",
            "Add one [[item]] table per plan item and retry")
    out: list[dict] = []
    seen: set[int] = set()
    for n, it in enumerate(raw, 1):
        unknown = set(it) - ITEM_KEYS
        if unknown:
            raise agent_help.no_report(
                f"{path}: item {n} has unknown key(s) {', '.join(sorted(unknown))} "
                f"(allowed: {', '.join(sorted(ITEM_KEYS))}).",
                "Fix the key names and retry; nothing was recorded")
        for k, v in it.items():
            if not isinstance(v, str):
                raise agent_help.no_report(
                    f"{path}: item {n} key {k!r} must be a string.",
                    "Quote the value and retry")
        stmt = it.get("statement", "").strip()
        if not stmt or "\n" in stmt:
            raise agent_help.no_report(
                f"{path}: item {n} needs a one-line statement.",
                "Give every item a one-line statement and retry")
        kind = it.get("kind", "deliverable")
        if kind not in KINDS:
            raise agent_help.no_report(
                f"{path}: item {n} has kind {kind!r} (want deliverable or exclusion).",
                "Fix the kind and retry")
        seq = parse_label(it["id"]) if "id" in it else None
        if seq is not None:
            if seq in seen:
                raise agent_help.no_report(f"{path}: {label(seq)} appears twice.",
                                           "Keep one table per item and retry")
            seen.add(seq)
        out.append({"seq": seq, "kind": kind, "statement": stmt,
                    "verify": it.get("verify", "").strip(),
                    "detail": it.get("detail", "").strip()})
    return out


def ops_for_items_file(task_id: int, wanted: list[dict]) -> list[dict]:
    """The operations that make a task's active items equal the file's.

    An item with an id is kept, and edited where it differs; an item without one
    is added; an active item the file leaves out is withdrawn. The file's order
    becomes the display order.
    """
    current = {it["seq"]: it for it in items(task_id)}
    known = {it["seq"] for it in items(task_id, all_items=True)}
    ops: list[dict] = []
    for w in wanted:
        if w["seq"] is not None and w["seq"] not in known:
            raise agent_help.no_report(
                f"E-{task_id} has no plan item {label(w['seq'])}.",
                "Drop the id to add it as a new item, or fix the number")
        if w["seq"] is not None and w["seq"] not in current:
            raise agent_help.no_report(
                f"{label(w['seq'])} is withdrawn; it cannot come back under its "
                "old number.",
                "Drop the id to add it as a new item")
    keep = {w["seq"] for w in wanted if w["seq"] is not None}
    for seq in current:
        if seq not in keep:
            ops.append({"op": "withdraw", "seq": seq})
    # The file's order becomes the display order. Sort orders start past every
    # existing one, so a kept item's new position never collides with another's.
    base = max([it["sort_order"] for it in items(task_id, all_items=True)] or [0])
    for pos, w in enumerate(wanted, 1):
        if w["seq"] is None:
            op = {"op": "add", "kind": w["kind"], "statement": w["statement"],
                  "sort_order": base + pos}
            if w["verify"]:
                op["verify"] = w["verify"]
            if w["detail"]:
                op["detail"] = w["detail"]
            ops.append(op)
            continue
        cur = current[w["seq"]]
        edit: dict = {"op": "edit", "seq": w["seq"]}
        if w["kind"] != cur["kind"]:
            edit["kind"] = w["kind"]
        if w["statement"] != cur["statement"]:
            edit["statement"] = w["statement"]
        if w["verify"] != (cur.get("verify") or ""):
            edit["verify"] = w["verify"]
        if w["detail"] != (cur.get("detail") or ""):
            edit["detail"] = w["detail"]
        if len(edit) > 2:
            ops.append(edit)
        ops.append({"op": "move", "seq": w["seq"], "sort_order": base + pos})
    # Loading a file identical to the item set (the mirror, say) changes nothing.
    if (all(o["op"] == "move" for o in ops)
            and [w["seq"] for w in wanted] == list(current)):
        return []
    return ops


def apply_items_file(task_id: int, path: str, keep_status: bool = False) -> None:
    ops = ops_for_items_file(task_id, parse_items_file(path))
    if ops:
        emit_ops(task_id, ops, keep_status=keep_status)


# Rendering -----------------------------------------------------------------

_VERDICT_WORDS = {
    "as_planned": "as planned", "differently": "done differently",
    "not_done": "not done", "blocked": "blocked",
    "kept": "kept", "not_kept": "NOT KEPT",
}


def render_items(rows: list[dict], *, verdicts: bool = True) -> str:
    """Items as lines: number, kind, statement, verify, and — when reconciled —
    the latest verdict and the answer to its question.
    """
    lines: list[str] = []
    for it in rows:
        tag = "excl" if it["kind"] == "exclusion" else "    "
        flags = []
        if it["origin"] == "grown":
            flags.append("grown")
        if it["state"] == "withdrawn":
            flags.append("withdrawn")
        elif it.get("approved_at") is None:
            flags.append("unapproved")
        elif (it["statement"] != it.get("approved_statement")
              or it["kind"] != it.get("approved_kind")):
            flags.append("changed since approval")
        flag = f" [{', '.join(flags)}]" if flags else ""
        lines.append(f"{it['label']:>4} {tag} {it['statement']}{flag}")
        if it.get("verify"):
            lines.append(f"{'':>9} verify: {it['verify']}")
        if it.get("detail"):
            lines.append(f"{'':>9} {it['detail']}")
        if verdicts and it.get("verdict"):
            v = _VERDICT_WORDS.get(it["verdict"], it["verdict"])
            line = f"{'':>9} ⟶ {v}"
            if it.get("note"):
                line += f": {it['note']}"
            lines.append(line)
            qid = it.get("question_id")
            if qid:
                status = it.get("question_status")
                if status == "answered":
                    lines.append(f"{'':>11} EQ-{qid} answered: {it['answer']}")
                elif status == "open":
                    lines.append(f"{'':>11} EQ-{qid} open — waiting for the user")
                else:
                    lines.append(f"{'':>11} EQ-{qid} {status}: {it.get('close_reason') or ''}")
                if it.get("holds_reason"):
                    lines.append(f"{'':>11} kept: {it['holds_reason']}")
    return "\n".join(lines)


def list_items(task_id: int, all_items: bool, as_json: bool) -> None:
    rows = items(task_id, all_items=all_items)
    if as_json:
        click.echo(json.dumps(rows, indent=2))
        return
    if not rows:
        click.echo(f"E-{task_id} has no plan items.")
        return
    click.echo(render_items(rows))


# Reconciliation ------------------------------------------------------------

def task_repo(task_id: int) -> Path:
    """Where the task's work is: its worktree, else its project's checkout."""
    from endless.task_cmd import _main_root_for_task, _worktree_for_task
    wt = _worktree_for_task(task_id)
    if wt is not None:
        return wt
    root = _main_root_for_task(task_id)
    if root is None:
        raise agent_help.no_report(f"No task E-{task_id}.", "Check the id and retry")
    return root


def parse_reconcile_file(path: str) -> list[dict]:
    """Read a reconciliation file: one `[[entry]]` table per active item.

        [[entry]]
        item = "P3"
        verdict = "differently"   # deliverable: as_planned | differently | not_done | blocked
                                  # exclusion:   kept | not_kept
        note = "How it differs, what is missing, or what blocked it."
        answer_holds = true       # with an earlier answer: it still holds, and why
        reask = true              # ...or it no longer holds, and what changed
        reason = "One line."
    """
    data = _load_toml(path, "reconciliation")
    extra = set(data) - {"entry"}
    if extra:
        raise agent_help.no_report(
            f"{path}: unknown top-level key(s) {', '.join(sorted(extra))}; "
            "a reconciliation file holds only [[entry]] tables.",
            "Remove them and retry; nothing was recorded")
    out = []
    for n, e in enumerate(data.get("entry", []), 1):
        unknown = set(e) - ENTRY_KEYS
        if unknown:
            raise agent_help.no_report(
                f"{path}: entry {n} has unknown key(s) {', '.join(sorted(unknown))} "
                f"(allowed: {', '.join(sorted(ENTRY_KEYS))}).",
                "Fix the key names and retry; nothing was recorded")
        if "item" not in e or "verdict" not in e:
            raise agent_help.no_report(
                f"{path}: entry {n} needs an item and a verdict.",
                "Add them and retry")
        out.append({
            "item_seq": parse_label(e["item"]),
            "verdict": str(e["verdict"]),
            "note": str(e.get("note", "")).strip(),
            "answer_holds": bool(e.get("answer_holds", False)),
            "reask": bool(e.get("reask", False)),
            "reason": str(e.get("reason", "")).strip(),
        })
    return out


def show_reconcile_context(task_id: int) -> None:
    """What the agent reads before it reconciles: every item, its earlier verdict
    and answer, what changed since across the whole task, and a file to fill in.
    """
    repo = task_repo(task_id)
    ctx = _go(["reconcile-context", "--id", str(task_id), "--repo", str(repo)])
    active = [it for it in ctx["items"] if it["state"] == "active"]
    if not active:
        raise agent_help.no_report(
            f"E-{task_id} has no active plan items to reconcile against.",
            f"Write them first: `endless task plan add E-{task_id} \"<statement>\"` "
            f"or `endless task update E-{task_id} --items-file <file>`")
    click.echo(click.style(f"Reconciling E-{task_id}", bold=True))
    last = ctx.get("last")
    if last:
        click.echo(f"Last reconciliation: R{last['seq']} at {last['created_at']}")
        changed = []
        if ctx.get("plan_reapproved"):
            changed.append("the plan was RE-APPROVED since — weigh every earlier "
                           "answer against the new plan")
        if ctx.get("plan_edited"):
            changed.append("plan items were edited since")
        if ctx.get("files_changed"):
            changed.append(f"{len(ctx['files_changed'])} file(s) changed since: "
                           + ", ".join(ctx["files_changed"][:20])
                           + (" …" if len(ctx["files_changed"]) > 20 else ""))
        if ctx.get("files_error"):
            changed.append(f"(could not list changed files: {ctx['files_error']})")
        for c in changed or ["nothing recorded as changed since"]:
            click.echo(f"  - {c}")
    click.echo()
    click.echo(render_items(ctx["items"]))
    click.echo()
    click.echo(
        "For an item whose earlier question was answered and whose verdict is "
        "unchanged, judge the answer against the WHOLE task as it is now, not "
        "the item alone: if the task changed enough that the answer may no "
        "longer hold, set reask = true and say what changed; otherwise "
        "answer_holds = true and say why it still holds.")
    click.echo()
    click.echo("Fill in and record with "
               f"`endless task reconcile E-{task_id} --file <file>`:")
    click.echo()
    for it in active:
        verdict = "as_planned" if it["kind"] == "deliverable" else "kept"
        click.echo(f'[[entry]]\nitem = "{it["label"]}"   # {it["statement"]}\n'
                   f'verdict = "{verdict}"\nnote = ""\n')


def reconcile(task_id: int, path: str) -> None:
    """Record a reconciliation, then report what it filed in kind order."""
    entries = parse_reconcile_file(path)
    repo = task_repo(task_id)
    fp = _go(["fingerprint", "--repo", str(repo)])
    out = emit_event(
        kind="task.reconciled",
        project=_project(task_id),
        entity_type="task",
        entity_id=str(task_id),
        payload={"fingerprint": fp["fingerprint"], "head": fp["head"],
                 "entries": entries},
    ) or {}
    ids = out.get("ids") or []
    click.echo(click.style("•", fg="cyan")
               + f" Recorded reconciliation R{out.get('seq', '?')} of E-{task_id}")
    open_qs = [q for q in provenance.rows_of(_go(["task-questions", "--id", str(task_id)]))
               if q.get("kind")]
    if not open_qs:
        click.echo("No discrepancies are waiting. The gate checks the rest when "
                   "you set the status.")
        return
    if ids:
        click.echo(f"Filed {', '.join(ids)}.")
    click.echo()
    click.echo(click.style("Discrepancies waiting for the user, most concerning first:",
                           bold=True))
    for q in open_qs:
        click.echo(f"  EQ-{q['id']} {q['question']}")
    click.echo()
    click.echo("List these in that order as your final message and stop. After "
               "the user answers, record the answers, make any changes, reconcile "
               "again, and only then set the task to `unverified`.")


# The gate ------------------------------------------------------------------

def gate(task_id: int, mode: str = "handover", to: str | None = None) -> dict:
    repo = task_repo(task_id)
    args = ["reconcile-gate", "--id", str(task_id), "--repo", str(repo), "--mode", mode]
    if to:
        args += ["--to", to]
    return _go(args)


def require_gate(task_id: int, action: str, mode: str = "handover",
                 to: str | None = None) -> None:
    """Refuse `action` unless the task's reconciliation is current and settled.
    `to` is the status being moved to: when the lifecycle refuses that move
    itself, the gate stands aside so the lifecycle's refusal is the one shown."""
    res = gate(task_id, mode, to)
    if res.get("pass"):
        return
    problems = "\n".join(f"  - {p}" for p in res.get("problems") or [])
    raise agent_help.no_report(
        f"E-{task_id} cannot {action} yet; its reconciliation gate refused:\n{problems}",
        f"Reconcile with `endless task reconcile E-{task_id}` (it shows what to "
        "fill in), and stop for the user's answers to any discrepancy before "
        "setting it to `unverified`",
        text=f"E-{task_id} cannot {action} yet:\n{problems}",
    )


def require_deliverable(task_id: int, action: str) -> None:
    """Refuse `action` on a task with no active deliverable item when its type
    must be itemized or it has active items (only exclusions, say)."""
    r = readiness(task_id)
    if (r["must_itemize"] or r["active"]) and r["deliverables"] == 0:
        raise agent_help.no_report(
            f"E-{task_id} is a {r['type']} task with no active deliverable plan "
            f"item, so it cannot be {action}. Its plan must be itemized first.",
            f"Add the items with `endless task update E-{task_id} --items-file "
            "<file>` (or `endless task plan add`), then retry")
