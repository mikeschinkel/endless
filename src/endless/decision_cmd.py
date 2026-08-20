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
from endless.project_path import normalize
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

LEGAL_TYPES_BY_PAIR: dict[tuple[str, str], tuple[str, ...]] = {
    ("decision", "task"): ("documents", "cleans_up_by", "implemented_by", "relates_to"),
    ("decision", "decision"): ("reverses", "modifies", "documents", "relates_to"),
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
):
    """List decisions for a project (or all projects with --all)."""
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

    if as_json:
        import json
        out = [
            {
                "id": decision_id_display(row["id"]),
                "title": row["title"],
                "status": row["status"],
                "created": row["created_at"],
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {proj_name} decisions")
        for row in rows:
            prefix = f"[{row['project_name']}] " if show_all else ""
            click.echo(
                f"{decision_id_display(row['id'])} {row['status']} "
                f"{prefix}{row['title']}"
            )
        return

    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 80

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
        "d.origin_task_id, d.notes, d.rejection_reason, "
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
    return normalize(row[0]["path"])


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
        raise click.ClickException(
            f"{decision_id_display(decision_id)} status is {cur_status!r}; "
            f"only 'accepted' or 'rejected' decisions can be reconsidered."
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
