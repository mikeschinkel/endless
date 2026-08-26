"""Decision command logic — operates against the `decisions` and
`decision_relations` tables introduced by E-1378.

Display IDs use the `ED-` prefix to disambiguate decisions from tasks.

Per-pair relation_type vocabularies are enforced here so the link / unlink
dispatchers refuse illegal types with a message that lists the legal set.
"""

import os
import shutil
import subprocess
from datetime import datetime
from pathlib import Path

import click

from endless import db
from endless import rowcap
from endless.project_path import resolved
from endless.task_cmd import (
    _display_path,
    _format_timestamp,
    _resolve_project,
    task_id_display,
    validate_description,
)


# Display ID --------------------------------------------------------------

def decision_id_display(item_id: int) -> str:
    """Format a decision ID for display: ED-42."""
    return f"ED-{item_id}"


def kind_label(kind: str) -> str:
    """Capitalized label for a kind (used in echo lines)."""
    return "Decision" if kind == "decision" else "Task"


def id_display(kind: str, item_id: int) -> str:
    """Format an id with the right prefix for its kind."""
    return decision_id_display(item_id) if kind == "decision" else task_id_display(item_id)


# Relation-type vocabulary by pair (plan, "Relation-type vocabulary by pair")
# Inverse display names are NOT accepted on the user-facing surface — the
# `--type` value names the relation FROM the source TO the target, which is
# the natural reading order at the CLI ('decision link ED-42 --to E-1199
# --type documents' reads "decision documents task"). Inverse views are a
# read-time concern (in renderers), not an input-side concern.

# `supersedes` (E-1920) sits alongside `reverses` and `modifies` rather than
# reusing either, because the three say different things about the OLD
# decision. `reverses`: the new one asserts the opposite. `modifies`: the old
# one is partially in force still. `supersedes`: the old one no longer governs
# at all, whether or not the new one contradicts it — a restatement for changed
# circumstances supersedes without reversing. Only `supersedes` has a status to
# match (`superseded`), and `decision supersede` is the verb that sets it;
# linking alone never moves a status, exactly as `task link` never does.

LEGAL_TYPES_BY_PAIR: dict[tuple[str, str], tuple[str, ...]] = {
    ("decision", "task"): ("documents", "cleans_up_by", "implemented_by", "relates_to"),
    ("decision", "decision"): ("supersedes", "reverses", "modifies", "documents",
                               "relates_to"),
    ("task", "decision"): ("implements", "cleans_up", "documents", "relates_to"),
    # task → task uses CANONICAL_DEP_TYPES; the task dispatcher checks it
    # against the existing registry (it accepts inverse views too because
    # link_tasks resolves swap).
}


def require_legal_relation_type(
    source_kind: str, target_kind: str, relation_type: str
) -> None:
    """Raise ClickException if relation_type is illegal for this pair."""
    pair = (source_kind, target_kind)
    legal = LEGAL_TYPES_BY_PAIR.get(pair)
    if legal is None:
        raise click.ClickException(
            f"Unsupported relation pair: {source_kind}→{target_kind}"
        )
    if relation_type not in legal:
        raise click.ClickException(
            f"{relation_type!r} is not legal for {source_kind}→{target_kind}; "
            f"legal types: {', '.join(legal)}."
        )


# List --------------------------------------------------------------------

def list_decisions(
    project_name: str | None = None,
    show_all: bool = False,
    sort_by: str | None = None,
    llm: bool = False,
    as_json: bool = False,
    limit: int | None = None,
    no_limit: bool = False,
):
    """List decisions for a project (or all projects with --all)."""
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
    where = "WHERE 1=1"
    params: list = []
    if not show_all:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND d.project_id = ?"
        params.append(project_id)
    elif project_name:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND d.project_id = ?"
        params.append(project_id)
    else:
        proj_name = "all projects"

    sort_col_map = {
        "id": "d.id DESC",
        "created": "d.created_at DESC, d.id DESC",
        "title": "d.title",
    }
    order_by = sort_col_map.get(sort_by or "id", "d.id DESC")

    rows = db.query(
        f"SELECT d.id, d.title, d.description, d.status, d.created_at, "
        f"p.name as project_name "
        f"FROM decisions d JOIN projects p ON d.project_id = p.id "
        f"{where} ORDER BY {order_by}",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo(f"# {proj_name}\n(no decisions)")
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No decisions for "
                + click.style(proj_name, bold=True)
            )
        return

    rows, hidden = rowcap.cap_rows(rows, cap)

    # One batched lookup for the whole page, not one per row — and only for the
    # two modes that still carry the supersession: since E-2064 the human table
    # renders the bare status, so it must not pay for a query it never reads.
    superseders = (
        superseded_by_map(r["id"] for r in rows) if (as_json or llm) else {}
    )

    if as_json:
        import json
        out = [
            {
                "id": decision_id_display(row["id"]),
                "title": row["title"],
                "status": row["status"],
                "superseded_by": [
                    decision_id_display(i)
                    for i in superseders.get(row["id"], ())
                ],
                "created": row["created_at"],
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        rowcap.echo_footer(hidden, llm=True, err=True)
        return

    if llm:
        click.echo(f"# {proj_name} decisions")
        for row in rows:
            prefix = f"[{row['project_name']}] " if show_all else ""
            note = superseded_by_note(row["status"], superseders.get(row["id"]))
            click.echo(
                f"{decision_id_display(row['id'])} {row['status']}{note} "
                f"{prefix}{row['title']}"
            )
        rowcap.echo_footer(hidden, llm=True)
        return

    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 80

    # E-2064: the Status cell is the BARE status here. The ' (by ED-NNN)'
    # annotation belongs to `decision show`, which has one status and unlimited
    # width. A table has many rows sharing one column, so the longest cell is
    # charged to every row's title — a handful of superseded rows cannot cost
    # the whole table its titles for a fact any reader recovers by opening the
    # decision.
    id_w = max(2, max(len(decision_id_display(r["id"])) for r in rows))
    date_w = max(7, max(len(_format_timestamp(r["created_at"])) for r in rows))
    status_w = max(6, max(len(r["status"]) for r in rows))
    gap = "  "
    fixed_width = id_w + date_w + status_w + len(gap) * 3
    if show_all:
        proj_w = max(7, max(len(r["project_name"]) for r in rows))
        fixed_width += proj_w + len(gap)
    title_width = max(20, term_width - fixed_width)

    display_titles = []
    for row in rows:
        title = row["title"]
        if len(title) > title_width:
            title = title[: title_width - 1] + "…"
        display_titles.append(title)

    header = (
        f"{'ID':<{id_w}}{gap}{'Status':<{status_w}}{gap}{'Created':<{date_w}}"
    )
    sep = f"{'─'*id_w}{gap}{'─'*status_w}{gap}{'─'*date_w}"
    if show_all:
        header += f"{gap}{'Project':<{proj_w}}"
        sep += f"{gap}{'─'*proj_w}"
    max_title_len = max(len(t) for t in display_titles) if display_titles else 5
    header += f"{gap}Title"
    sep += f"{gap}{'─'*max_title_len}"
    click.echo(header)
    click.echo(sep)

    for row, title in zip(rows, display_titles):
        line = (
            f"{decision_id_display(row['id']):<{id_w}}{gap}"
            f"{row['status']:<{status_w}}{gap}"
            f"{_format_timestamp(row['created_at']):<{date_w}}"
        )
        if show_all:
            line += f"{gap}{row['project_name']:<{proj_w}}"
        line += f"{gap}{title}"
        click.echo(line)
    rowcap.echo_footer(hidden)


# Show --------------------------------------------------------------------

def _fetch_decision_relations(decision_id: int) -> list[dict]:
    """Return decision_relations rows where this decision is the source,
    plus inbound rows (this decision is the target) from decision_relations
    AND task_deps. Each row is shaped: {direction, kind, id, rel}."""
    out: list[dict] = []

    # Outbound: decision_relations rows sourced by this decision.
    for r in db.query(
        "SELECT target_kind, target_id, relation_type "
        "FROM decision_relations WHERE source_decision_id = ? "
        "ORDER BY relation_type, target_id",
        (decision_id,),
    ):
        out.append(
            {
                "direction": "out",
                "kind": r["target_kind"],
                "id": r["target_id"],
                "rel": r["relation_type"],
            }
        )

    # Inbound: other decisions pointing at this one.
    for r in db.query(
        "SELECT source_decision_id, relation_type "
        "FROM decision_relations "
        "WHERE target_kind = 'decision' AND target_id = ? "
        "ORDER BY relation_type, source_decision_id",
        (decision_id,),
    ):
        out.append(
            {
                "direction": "in",
                "kind": "decision",
                "id": r["source_decision_id"],
                "rel": r["relation_type"],
            }
        )

    # Inbound: tasks pointing at this decision via task_deps.
    for r in db.query(
        "SELECT source_id, dep_type FROM task_deps "
        "WHERE source_type = 'task' AND target_type = 'decision' AND target_id = ? "
        "ORDER BY dep_type, source_id",
        (decision_id,),
    ):
        out.append(
            {
                "direction": "in",
                "kind": "task",
                "id": r["source_id"],
                "rel": r["dep_type"],
            }
        )
    return out


def detail_decision(item_id: int, llm: bool = False, as_json: bool = False):
    """Show full detail for a decision."""
    row = db.query(
        "SELECT d.id, d.title, d.description, d.text, d.status, "
        "d.origin_task_id, d.notes, d.rejection_reason, d.obsolete_reason, "
        "d.created_at, d.updated_at, p.name as project_name "
        "FROM decisions d JOIN projects p ON d.project_id = p.id "
        "WHERE d.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No decision found with id {decision_id_display(item_id)}"
        )
    item = row[0]
    relations = _fetch_decision_relations(item_id)
    # Named on its own line rather than left to the reader to spot among the
    # links: "superseded" without the successor is the dead end E-1920 exists
    # to close, so the one field that resolves it does not get buried.
    superseders = superseded_by_map([item_id]).get(item_id, [])

    if as_json:
        import json
        out = {
            "id": decision_id_display(item["id"]),
            "title": item["title"],
            "project": item["project_name"],
            "status": item["status"],
            "origin_task": (
                task_id_display(item["origin_task_id"])
                if item["origin_task_id"] else None
            ),
            "rejection_reason": item["rejection_reason"] or None,
            "obsolete_reason": item["obsolete_reason"] or None,
            "superseded_by": [decision_id_display(i) for i in superseders],
            "description": item["description"] or None,
            "text": item["text"] or None,
            "notes": item["notes"] or None,
            "created": item["created_at"],
            "updated": item["updated_at"],
            "relations": [
                {
                    "direction": r["direction"],
                    "kind": r["kind"],
                    "id": id_display(r["kind"], r["id"]),
                    "type": r["rel"],
                }
                for r in relations
            ],
        }
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {decision_id_display(item['id'])} {item['title']}")
        click.echo(f"project={item['project_name']}")
        click.echo(f"status={item['status']}")
        if item["origin_task_id"]:
            click.echo(f"origin_task={task_id_display(item['origin_task_id'])}")
        if item["rejection_reason"]:
            click.echo(f"rejection_reason={item['rejection_reason']}")
        if item["obsolete_reason"]:
            click.echo(f"obsolete_reason={item['obsolete_reason']}")
        if superseders:
            click.echo(
                "superseded_by="
                + ",".join(decision_id_display(i) for i in superseders)
            )
        for rel in relations:
            arrow = "→" if rel["direction"] == "out" else "←"
            click.echo(
                f"link {arrow} {id_display(rel['kind'], rel['id'])} ({rel['rel']})"
            )
        click.echo(f"created={item['created_at']}")
        click.echo(f"updated={item['updated_at']}")
        if item["description"]:
            click.echo(f"\n## Description\n{item['description']}")
        if item["text"]:
            click.echo(f"\n## Text\n{item['text']}")
        return

    # Human-readable output (mirrors detail_item's shape but no phase /
    # outcome / source_file / completed_at / tier — decisions don't have them).
    col_w = 11
    label = lambda s: click.style(f"{s:<{col_w}}", fg="cyan")
    val = lambda s: click.style(str(s), fg="white", bold=True)

    click.echo()
    click.echo(click.style("Decision Detail", fg="green", bold=True))
    click.echo(click.style("───────────────", dim=True))
    click.echo(f"{label('ID:')} {val(decision_id_display(item['id']))}")
    click.echo(f"{label('Title:')} {val(item['title'])}")
    click.echo(f"{label('Project:')} {val(item['project_name'])}")
    click.echo(f"{label('Status:')} {val(item['status'])}")
    if item["origin_task_id"]:
        click.echo(
            f"{label('Origin:')} {val(task_id_display(item['origin_task_id']))}"
        )
    if item["rejection_reason"]:
        click.echo(f"{label('Reason:')} {val(item['rejection_reason'])}")
    if item["obsolete_reason"]:
        click.echo(f"{label('Obsolete:')} {val(item['obsolete_reason'])}")
    if superseders:
        click.echo(
            f"{label('Superseded:')} "
            + val(", ".join(decision_id_display(i) for i in superseders))
        )
    click.echo(
        f"{label('Created:')} {val(_format_timestamp(item['created_at']))}"
    )
    if item["updated_at"] and item["updated_at"] != item["created_at"]:
        click.echo(
            f"{label('Updated:')} {val(_format_timestamp(item['updated_at']))}"
        )

    if relations:
        click.echo(click.style("Links:", fg="cyan"))
        for rel in relations:
            arrow = "→" if rel["direction"] == "out" else "←"
            click.echo(
                f"  {arrow} {id_display(rel['kind'], rel['id'])} ({rel['rel']})"
            )

    if item["description"]:
        click.echo()
        click.echo(click.style("— Description —", fg="cyan"))
        click.echo(item["description"])

    if item["text"]:
        click.echo()
        click.echo(click.style("— Text —", fg="cyan"))
        click.echo(item["text"])

    click.echo()


# Add ---------------------------------------------------------------------

def _main_root_for_project(project_id: int) -> Path | None:
    """Registered main-checkout root of a project, or None."""
    row = db.query(
        "SELECT path FROM projects WHERE id = ? LIMIT 1", (project_id,),
    )
    if not row:
        return None
    return resolved(row[0]["path"])


def _mirror_decision_body(
    decision_id: int, project_id: int, body: str, action: str = "add"
) -> None:
    """Write+commit `.endless/decisions/ED-NNN.md` from a decision body (E-1747).

    Decisions have no worktree of their own, so the mirror lands in the
    current task worktree when `decision add`/`update` runs inside one (riding
    that worktree's land), else on the project's main checkout — the same place
    the decision's ledger entry is already committed. The DB row stays the
    source of truth; this is the durability belt. `update` (E-1533) re-emits
    the mirror so it doesn't desync when a decision's body is edited in place.
    Best-effort: a missing endless-go binary or a git failure warns and skips
    rather than aborting the decision.
    """
    from endless.worktree_cmd import worktree_root_for_cwd, _commit_doc_in_worktree

    rel_path = f".endless/decisions/ED-{decision_id}.md"
    subject = f"Endless: {action} decision ED-{decision_id}"

    wt = worktree_root_for_cwd()
    if wt is not None:
        target = wt / rel_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body)
        click.echo(
            click.style("✓", fg="green")
            + f" Wrote decision to {_display_path(target)}"
        )
        _commit_doc_in_worktree(wt, rel_path, subject)
        return

    root = _main_root_for_project(project_id)
    if root is None:
        return
    target = root / rel_path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(body)
    click.echo(
        click.style("✓", fg="green")
        + f" Wrote decision to {_display_path(target)}"
    )
    _commit_doc_on_main(root, rel_path, subject)


def _commit_doc_on_main(project_root: Path, rel_path: str, subject: str) -> None:
    """Commit one doc mirror on the project's main checkout via endless-go.

    Reuses the Go `event commit-doc` path (→ events.CommitDoc → commitPaths),
    inheriting its main-checkout enforcement and GIT_DIR-family env stripping
    instead of re-implementing them in Python. Warns and skips on any failure.
    """
    binary = shutil.which("endless-go")
    if not binary:
        click.echo(
            "  warning: endless-go not found on PATH; decision file "
            "not committed to main.",
            err=True,
        )
        return
    try:
        result = subprocess.run(
            [binary, "event", "commit-doc", "--project-root", str(project_root),
             "--path", rel_path, "--subject", subject],
            capture_output=True, text=True,
        )
    except OSError as e:
        click.echo(f"  warning: endless-go event commit-doc: {e}", err=True)
        return
    if result.returncode != 0:
        click.echo(
            f"  warning: could not commit {rel_path} to main: "
            f"{(result.stderr or '').strip()}",
            err=True,
        )


def add_decision(
    title: str,
    description: str | None = None,
    project_name: str | None = None,
    about_task_ids: tuple[int, ...] = (),
    decides_task_ids: tuple[int, ...] = (),
) -> int | None:
    """Record a decision and any --about / --decides links.

    Emits decision.created (assigns ED-ID + inserts into `decisions`), then
    one decision_relation.created per --about (decision documents task) and
    one task_deps row (target_type='decision') per --decides (task implements
    decision).

    Returns the new decision id, or None on emission failure (event_bridge
    raises ClickException; this is just for symmetry with add_item).
    """
    from endless.event_bridge import emit_event

    if title.lower().startswith("record that "):
        raise click.ClickException(
            "Decision titles should state the decision, not narrate recording it.\n"
            f"  Try: {title[len('record that '):]}"
        )
    validate_description(description)

    proj_id, proj_name = _resolve_project(project_name)

    payload: dict = {
        "title": title,
        "description": description or "",
        "status": "proposed",
    }

    result = emit_event(
        kind="decision.created",
        project=proj_name,
        entity_type="decision",
        entity_id="0",
        payload=payload,
    )
    if result is None:
        return None
    new_id = int(result["id"].replace("ED-", ""))
    click.echo(
        click.style("•", fg="cyan")
        + f" Added {decision_id_display(new_id)}: {title}"
    )

    if description and description.strip():
        _mirror_decision_body(new_id, proj_id, description)

    for tid in about_task_ids:
        _emit_decision_relation_created(
            proj_name, new_id, "task", tid, "documents"
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Linked: Decision {decision_id_display(new_id)} "
            f"documents Task {task_id_display(tid)}"
        )

    for tid in decides_task_ids:
        # task IMPLEMENTS decision: source=task → target=decision (task_deps).
        _insert_task_decision_dep(tid, new_id, "implements")
        click.echo(
            click.style("•", fg="cyan")
            + f" Linked: Task {task_id_display(tid)} implements "
            f"Decision {decision_id_display(new_id)}"
        )

    return new_id


# Update ------------------------------------------------------------------

def update_decision(
    decision_id: int,
    title: str | None = None,
    description: str | None = None,
) -> None:
    """Edit a decision's title and/or description in place (E-1533).

    Emits decision.fields_updated (no new ID, no status change) — this
    replaces the reject+re-add workaround that left a misleading rejected row.
    Editable in any status: title/description are metadata, so correcting
    wording shouldn't require a status dance.

    When the description changes, the `.endless/decisions/ED-NNN.md` mirror is
    re-emitted from the new body. That mirror is a one-way durability copy;
    the DB row is the source of truth, so editing the file alone would silently
    desync (confirmed empirically) — the rewrite keeps them aligned.
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT d.id, d.project_id, p.name as project_name "
        "FROM decisions d JOIN projects p ON d.project_id = p.id WHERE d.id = ?",
        (decision_id,),
    )
    if not row:
        raise click.ClickException(
            f"No decision found with id {decision_id_display(decision_id)}"
        )

    fields: dict = {}
    if title is not None:
        if not title.strip():
            raise click.ClickException("--title may not be empty.")
        if title.lower().startswith("record that "):
            raise click.ClickException(
                "Decision titles should state the decision, not narrate recording it.\n"
                f"  Try: {title[len('record that '):]}"
            )
        fields["title"] = title
    if description is not None:
        validate_description(description)
        fields["description"] = description

    if not fields:
        raise click.ClickException(
            "Nothing to update. Specify --title and/or --description."
        )

    emit_event(
        kind="decision.fields_updated",
        project=row[0]["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={"fields": fields},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Updated {decision_id_display(decision_id)}: "
        + ", ".join(sorted(fields))
    )

    if "description" in fields:
        _mirror_decision_body(
            decision_id, row[0]["project_id"], description, action="update"
        )


# Accept / Reject ---------------------------------------------------------

def accept_decision(decision_id: int):
    """Mark a decision accepted (proposed → accepted)."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT d.id, d.status, p.name as project_name "
        "FROM decisions d JOIN projects p ON d.project_id = p.id WHERE d.id = ?",
        (decision_id,),
    )
    if not row:
        raise click.ClickException(
            f"No decision found with id {decision_id_display(decision_id)}"
        )
    cur_status = row[0]["status"]
    if cur_status != "proposed":
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'proposed' decisions can be accepted."
        )

    emit_event(
        kind="decision.accepted",
        project=row[0]["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Accepted {decision_id_display(decision_id)}"
    )


def reject_decision(decision_id: int, reason: str):
    """Mark a decision rejected (proposed → rejected) with a stored reason."""
    from endless.event_bridge import emit_event

    if not reason or not reason.strip():
        raise click.ClickException("--reason is required and may not be empty.")

    row = db.query(
        "SELECT d.id, d.status, p.name as project_name "
        "FROM decisions d JOIN projects p ON d.project_id = p.id WHERE d.id = ?",
        (decision_id,),
    )
    if not row:
        raise click.ClickException(
            f"No decision found with id {decision_id_display(decision_id)}"
        )
    cur_status = row[0]["status"]
    if cur_status != "proposed":
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'proposed' decisions can be rejected."
        )

    emit_event(
        kind="decision.rejected",
        project=row[0]["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={"reason": reason},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Rejected {decision_id_display(decision_id)}: {reason}"
    )


# Unaccept / Unreject / Reconsider ----------------------------------------
#
# E-1864. `unaccept` and `unreject` are the exact inverses of `accept` and
# `reject`: each refuses unless the decision is in the status it undoes, so
# aiming one at the wrong terminal status errors instead of silently
# performing the other reversal. `reconsider` is the status-agnostic
# convenience that dispatches to whichever applies.
#
# Deliberately not named `reopen` (the task-tree verb): a decision is proposed
# and then accepted or rejected — it is never "open", so there is nothing to
# re-open.

def _fetch_decision_for_status_change(decision_id: int) -> dict:
    """Row (id, status, project_name) for a status verb, or ClickException."""
    row = db.query(
        "SELECT d.id, d.status, p.name as project_name "
        "FROM decisions d JOIN projects p ON d.project_id = p.id WHERE d.id = ?",
        (decision_id,),
    )
    if not row:
        raise click.ClickException(
            f"No decision found with id {decision_id_display(decision_id)}"
        )
    return row[0]


def unaccept_decision(decision_id: int):
    """Revert an accepted decision to proposed (accepted → proposed)."""
    from endless.event_bridge import emit_event

    row = _fetch_decision_for_status_change(decision_id)
    cur_status = row["status"]
    if cur_status != "accepted":
        hint = (
            " Use `endless decision unreject` instead."
            if cur_status == "rejected" else ""
        )
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'accepted' decisions can be unaccepted.{hint}"
        )

    emit_event(
        kind="decision.unaccepted",
        project=row["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Unaccepted {decision_id_display(decision_id)} (accepted → proposed)"
    )


def unreject_decision(decision_id: int):
    """Revert a rejected decision to proposed (rejected → proposed).

    Clears the stored rejection_reason — it explains why the decision was
    rejected, and a decision back in `proposed` has not been rejected. The
    reason stays recoverable from the `decision.rejected` ledger entry.
    """
    from endless.event_bridge import emit_event

    row = _fetch_decision_for_status_change(decision_id)
    cur_status = row["status"]
    if cur_status != "rejected":
        hint = (
            " Use `endless decision unaccept` instead."
            if cur_status == "accepted" else ""
        )
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'rejected' decisions can be unrejected.{hint}"
        )

    emit_event(
        kind="decision.unrejected",
        project=row["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Unrejected {decision_id_display(decision_id)} (rejected → proposed); "
        f"reason cleared"
    )


def reconsider_decision(decision_id: int):
    """Revert a decision from either terminal status back to proposed.

    Convenience dispatcher over unaccept / unreject for when you don't care
    (or don't recall) which way the decision went. Use the precise verb when
    you want the wrong-status guard.
    """
    row = _fetch_decision_for_status_change(decision_id)
    cur_status = row["status"]
    if cur_status == "accepted":
        unaccept_decision(decision_id)
    elif cur_status == "rejected":
        unreject_decision(decision_id)
    else:
        # E-1920's end states are deliberately NOT folded in here. Reconsider
        # means "put it back on the table", and a retired decision has to
        # regain force before it can be argued about again — otherwise
        # reinstating a mis-aimed supersede would silently discard the accept
        # it should return to.
        hint = (
            f" Use `endless decision reinstate "
            f"{decision_id_display(decision_id)}` to put it back in force."
            if cur_status in _END_STATUSES else ""
        )
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'accepted' or 'rejected' decisions can be reconsidered."
            f"{hint}"
        )


# End states: supersede / obsolete / reinstate (E-1920) -------------------
#
# A decision that stopped governing used to be inexpressible: the vocabulary
# ended at accepted|rejected, so a rule overtaken years ago still read as
# current, and nothing in the ledger could settle a dispute about it either
# way. These two end states split the reason it stopped.
#
# Both are reachable only from `accepted`, which is the whole of their meaning:
# only an accepted decision governs, so only an accepted decision can stop.
# That also makes `reinstate` unambiguous — one destination, no stored prior
# status — which is why these three verbs are not the E-1864 shape of one
# reversal per forward transition.

# The two end states, and the ONE status they return to. Kept as names rather
# than inlined so a reader adding a third end state sees every place that has
# to agree.
_END_STATUSES = ("superseded", "obsolete")
_GOVERNING_STATUS = "accepted"


def superseded_by_map(decision_ids) -> dict[int, list[int]]:
    """Map each id to the ids of the decisions that supersede it.

    `old superseded by new` is stored active-voice as (source=new,
    target=old, relation_type='supersedes'), so a decision's replacements are
    the source_decision_ids of the `supersedes` rows pointing AT it.

    Batched over the whole id set (mirroring task_cmd.replaced_by_map): this
    feeds the list renderer, which would otherwise issue a query per row.
    """
    ids = list(decision_ids)
    if not ids:
        return {}
    placeholders = ",".join("?" for _ in ids)
    rows = db.query(
        "SELECT target_id AS old_id, source_decision_id AS new_id "
        "FROM decision_relations "
        "WHERE target_kind = 'decision' AND relation_type = 'supersedes' "
        f"AND target_id IN ({placeholders}) "
        "ORDER BY source_decision_id",
        tuple(ids),
    )
    out: dict[int, list[int]] = {}
    for row in rows:
        out.setdefault(row["old_id"], []).append(row["new_id"])
    return out


def superseded_by_note(status: str | None, ids: list[int] | None) -> str:
    """The inline ' (by ED-NNN)' annotation for a status display, or ''.

    Rendered ONLY alongside `superseded`, mirroring task_cmd.replaced_by_note:
    that is the one status which reads as the end of the story while leaving
    the reader unable to recover WHAT took over. Every other status either
    names its own reason or has none to name, so annotating them would be
    noise in a column that has to stay narrow.
    """
    if not ids or status != "superseded":
        return ""
    return " (by " + ", ".join(decision_id_display(i) for i in ids) + ")"


def _require_governing(decision_id: int, row: dict, verb: str) -> None:
    """Refuse an end-state transition on anything but `accepted`.

    The message names the status found and why the transition does not apply
    to it, because the three wrong statuses fail for three different reasons
    and a bare "expected accepted" would leave the caller guessing which.
    """
    cur = row["status"]
    if cur == _GOVERNING_STATUS:
        return
    disp = decision_id_display(decision_id)
    if cur == "proposed":
        why = (
            f"it was never accepted, so it never governed anything. Accept it "
            f"first, or `endless decision reject {disp} --reason ...` if it is "
            f"being turned down."
        )
    elif cur == "rejected":
        why = (
            "it was rejected, so it never took effect — there is nothing to "
            "retire."
        )
    elif cur in _END_STATUSES:
        why = (
            f"it is already {cur}. Run `endless decision reinstate {disp}` "
            f"first if that end state is wrong."
        )
    else:
        why = f"only {_GOVERNING_STATUS!r} decisions can be {verb}."
    raise click.ClickException(f"{disp} is {cur!r} — cannot {verb} it: {why}")


def supersede_decision(old_id: int, new_id: int):
    """Mark old_id superseded by new_id: record the relation, set the status.

    Two events, relation first, mirroring `task replace`. The relation is the
    authoritative record of WHICH decision took over — a status alone cannot
    carry a pointer, and "superseded" without a name is the same dead end as
    "accepted" on something that stopped governing.
    """
    from endless.event_bridge import emit_event

    if old_id == new_id:
        raise click.ClickException("A decision cannot supersede itself.")

    old_row = _fetch_decision_for_status_change(old_id)
    new_row = _fetch_decision_for_status_change(new_id)
    _require_governing(old_id, old_row, "supersede")

    # The replacement need not be `accepted` yet — superseding on the strength
    # of a still-proposed successor is ordinary, and gating it would force the
    # two steps into an order the work does not have. It must not be finished
    # with, though: pointing at a rejected or retired decision would leave the
    # old one closed with a successor that never governs, which is strictly
    # worse than leaving it accepted.
    if new_row["status"] in ("rejected",) + _END_STATUSES:
        raise click.ClickException(
            f"{decision_id_display(new_id)} is {new_row['status']!r} — it "
            f"cannot supersede anything, because it does not govern.\n"
            f"Point {decision_id_display(old_id)} at a decision that is "
            f"proposed or accepted."
        )

    try:
        link_decision(new_id, "decision", old_id, "supersedes")
    except click.ClickException as e:
        if "already" in str(e).lower():
            raise click.ClickException(
                f"{decision_id_display(old_id)} is already superseded by "
                f"{decision_id_display(new_id)}."
            )
        raise

    emit_event(
        kind="decision.superseded",
        project=old_row["project_name"],
        entity_type="decision",
        entity_id=str(old_id),
        payload={"by_superseding_id": new_id},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Superseded {decision_id_display(old_id)} "
        f"by {decision_id_display(new_id)} (accepted → superseded)"
    )


def obsolete_decision(decision_id: int, reason: str):
    """Mark a decision obsolete (accepted → obsolete) with a stored reason.

    `--reason` is required for the same reason `reject --reason` is: this is
    the only field that distinguishes a rule deliberately retired from one
    that quietly stopped being mentioned, and it is what a reader hitting the
    decision later needs in order to stop re-litigating it.
    """
    from endless.event_bridge import emit_event

    if not reason or not reason.strip():
        raise click.ClickException("--reason is required and may not be empty.")

    row = _fetch_decision_for_status_change(decision_id)
    _require_governing(decision_id, row, "obsolete")

    emit_event(
        kind="decision.obsoleted",
        project=row["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={"reason": reason},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Obsoleted {decision_id_display(decision_id)} "
        f"(accepted → obsolete): {reason}"
    )


def reinstate_decision(decision_id: int):
    """Put a retired decision back in force (superseded | obsolete → accepted).

    Retires the `supersedes` relation on the way, mirroring how `unreject`
    clears `rejection_reason`: a decision back in `accepted` has not been
    superseded, and leaving the row would reproduce the exact contradiction
    E-1920 set out to remove — a live decision carrying a successor. Both facts
    stay recoverable from the ledger entries that recorded them.

    For correcting the record — a mis-aimed supersede, a retirement that turned
    out to be premature. A decision rightly retired and now genuinely back in
    force is better recorded as a NEW decision, so the gap in which it did not
    apply stays visible.
    """
    from endless.event_bridge import emit_event

    row = _fetch_decision_for_status_change(decision_id)
    cur_status = row["status"]
    if cur_status not in _END_STATUSES:
        hint = (
            " Use `endless decision reconsider` to take an accepted or "
            "rejected decision back to proposed."
            if cur_status in ("accepted", "rejected") else ""
        )
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only {' or '.join(repr(s) for s in _END_STATUSES)} decisions "
            f"can be reinstated.{hint}"
        )

    for superseder_id in superseded_by_map([decision_id]).get(decision_id, ()):
        unlink_decision(superseder_id, "decision", decision_id, "supersedes")

    emit_event(
        kind="decision.reinstated",
        project=row["project_name"],
        entity_type="decision",
        entity_id=str(decision_id),
        payload={},
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Reinstated {decision_id_display(decision_id)} "
        f"({cur_status} → accepted)"
    )


# Link / Unlink (decision-sourced dispatcher) -----------------------------

def link_decision(
    source_decision_id: int,
    target_kind: str,
    target_id: int,
    relation_type: str,
):
    """Create a decision-sourced relation (decision → task or decision)."""
    from endless.event_bridge import emit_event

    require_legal_relation_type("decision", target_kind, relation_type)

    if not db.exists("SELECT 1 FROM decisions WHERE id = ?", (source_decision_id,)):
        raise click.ClickException(
            f"Decision {decision_id_display(source_decision_id)} not found."
        )
    if target_kind == "decision":
        if source_decision_id == target_id:
            raise click.ClickException("A decision cannot link to itself.")
        if not db.exists("SELECT 1 FROM decisions WHERE id = ?", (target_id,)):
            raise click.ClickException(
                f"Decision {decision_id_display(target_id)} not found."
            )
    elif target_kind == "task":
        if not db.exists("SELECT 1 FROM live_tasks WHERE id = ?", (target_id,)):
            raise click.ClickException(
                f"Task {task_id_display(target_id)} not found."
            )

    # Pre-check uniqueness so we get a friendly error instead of an executor
    # IntegrityError after the JSONL line has been written.
    if db.exists(
        "SELECT 1 FROM decision_relations "
        "WHERE source_decision_id = ? AND target_kind = ? "
        "AND target_id = ? AND relation_type = ?",
        (source_decision_id, target_kind, target_id, relation_type),
    ):
        raise click.ClickException(
            f"{decision_id_display(source_decision_id)} is already linked to "
            f"{id_display(target_kind, target_id)} as {relation_type!r}."
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="decision_relation.created",
        project=proj_name,
        entity_type="decision_relation",
        entity_id="0",
        payload={
            "source_decision_id": source_decision_id,
            "target_kind": target_kind,
            "target_id": target_id,
            "relation_type": relation_type,
        },
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Linked: Decision {decision_id_display(source_decision_id)} "
        f"{relation_type} {kind_label(target_kind)} "
        f"{id_display(target_kind, target_id)}"
    )


def unlink_decision(
    source_decision_id: int,
    target_kind: str,
    target_id: int,
    relation_type: str | None = None,
):
    """Remove a decision-sourced relation. If relation_type is None and
    exactly one matching row exists, drop it; if multiple, refuse and list."""
    from endless.event_bridge import emit_event

    if relation_type is None:
        rows = db.query(
            "SELECT relation_type FROM decision_relations "
            "WHERE source_decision_id = ? AND target_kind = ? AND target_id = ?",
            (source_decision_id, target_kind, target_id),
        )
        if not rows:
            raise click.ClickException(
                f"No relation: {decision_id_display(source_decision_id)} → "
                f"{id_display(target_kind, target_id)}"
            )
        if len(rows) > 1:
            types = ", ".join(r["relation_type"] for r in rows)
            raise click.ClickException(
                f"Multiple relations between "
                f"{decision_id_display(source_decision_id)} and "
                f"{id_display(target_kind, target_id)} ({types}). "
                f"Specify --type <type>."
            )
        relation_type = rows[0]["relation_type"]
    else:
        require_legal_relation_type("decision", target_kind, relation_type)
        if not db.exists(
            "SELECT 1 FROM decision_relations "
            "WHERE source_decision_id = ? AND target_kind = ? "
            "AND target_id = ? AND relation_type = ?",
            (source_decision_id, target_kind, target_id, relation_type),
        ):
            raise click.ClickException(
                f"No {relation_type!r} relation: "
                f"{decision_id_display(source_decision_id)} → "
                f"{id_display(target_kind, target_id)}"
            )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="decision_relation.deleted",
        project=proj_name,
        entity_type="decision_relation",
        entity_id="0",
        payload={
            "source_decision_id": source_decision_id,
            "target_kind": target_kind,
            "target_id": target_id,
            "relation_type": relation_type,
        },
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Unlinked: Decision {decision_id_display(source_decision_id)} "
        f"{relation_type} {kind_label(target_kind)} "
        f"{id_display(target_kind, target_id)}"
    )


# Task-sourced helpers used by add_decision / task link dispatcher --------

def _emit_decision_relation_created(
    project_name: str,
    source_decision_id: int,
    target_kind: str,
    target_id: int,
    relation_type: str,
):
    """Emit decision_relation.created (used internally by add_decision)."""
    from endless.event_bridge import emit_event

    emit_event(
        kind="decision_relation.created",
        project=project_name,
        entity_type="decision_relation",
        entity_id="0",
        payload={
            "source_decision_id": source_decision_id,
            "target_kind": target_kind,
            "target_id": target_id,
            "relation_type": relation_type,
        },
    )


def _insert_task_decision_dep(
    source_task_id: int,
    target_decision_id: int,
    dep_type: str,
):
    """Insert a task → decision row into task_deps. Direct INSERT mirrors
    link_tasks's pattern; the dedicated task_deps event lands with E-1389's
    rename. UNIQUE-violation is pre-checked so the user gets a friendly
    error instead of a raw IntegrityError."""
    if db.exists(
        "SELECT 1 FROM task_deps "
        "WHERE source_type = 'task' AND source_id = ? "
        "AND target_type = 'decision' AND target_id = ? AND dep_type = ?",
        (source_task_id, target_decision_id, dep_type),
    ):
        raise click.ClickException(
            f"{task_id_display(source_task_id)} is already linked to "
            f"{decision_id_display(target_decision_id)} as {dep_type!r}."
        )
    db.execute(
        "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
        "VALUES ('task', ?, 'decision', ?, ?)",
        (source_task_id, target_decision_id, dep_type),
    )


def link_task_to_decision(
    source_task_id: int,
    target_decision_id: int,
    dep_type: str,
):
    """Link a task → decision (writes a task_deps row with target_type='decision')."""
    require_legal_relation_type("task", "decision", dep_type)
    if not db.exists("SELECT 1 FROM live_tasks WHERE id = ?", (source_task_id,)):
        raise click.ClickException(
            f"Task {task_id_display(source_task_id)} not found."
        )
    if not db.exists("SELECT 1 FROM decisions WHERE id = ?", (target_decision_id,)):
        raise click.ClickException(
            f"Decision {decision_id_display(target_decision_id)} not found."
        )
    _insert_task_decision_dep(source_task_id, target_decision_id, dep_type)
    click.echo(
        click.style("•", fg="cyan")
        + f" Linked: Task {task_id_display(source_task_id)} {dep_type} "
        f"Decision {decision_id_display(target_decision_id)}"
    )


def unlink_task_from_decision(
    source_task_id: int,
    target_decision_id: int,
    dep_type: str | None = None,
):
    """Remove a task → decision link from task_deps."""
    if dep_type is None:
        rows = db.query(
            "SELECT dep_type FROM task_deps WHERE source_type = 'task' "
            "AND source_id = ? AND target_type = 'decision' AND target_id = ?",
            (source_task_id, target_decision_id),
        )
        if not rows:
            raise click.ClickException(
                f"No relation: {task_id_display(source_task_id)} → "
                f"{decision_id_display(target_decision_id)}"
            )
        if len(rows) > 1:
            types = ", ".join(r["dep_type"] for r in rows)
            raise click.ClickException(
                f"Multiple relations between "
                f"{task_id_display(source_task_id)} and "
                f"{decision_id_display(target_decision_id)} ({types}). "
                f"Specify --type <type>."
            )
        dep_type = rows[0]["dep_type"]
    else:
        require_legal_relation_type("task", "decision", dep_type)

    result = db.execute(
        "DELETE FROM task_deps WHERE source_type = 'task' AND source_id = ? "
        "AND target_type = 'decision' AND target_id = ? AND dep_type = ?",
        (source_task_id, target_decision_id, dep_type),
    )
    if result.rowcount == 0:
        raise click.ClickException(
            f"No {dep_type!r} relation: {task_id_display(source_task_id)} → "
            f"{decision_id_display(target_decision_id)}"
        )
    click.echo(
        click.style("•", fg="cyan")
        + f" Unlinked: Task {task_id_display(source_task_id)} {dep_type} "
        f"Decision {decision_id_display(target_decision_id)}"
    )
