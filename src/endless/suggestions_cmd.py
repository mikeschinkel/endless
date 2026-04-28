"""Suggestions CLI: list, show, accept AI-agent rule-relaxation suggestions (E-918)."""

from __future__ import annotations

from endless import db
from endless.task_cmd import _resolve_project


def list_suggestions(project: str | None, show_all: bool, source: str | None) -> None:
    project_id, _ = _resolve_project(project)
    where = ["project_id = ?"]
    params: list = [project_id]
    if not show_all:
        where.append("task_id IS NULL")
    if source:
        where.append("source = ?")
        params.append(source)
    sql = (
        "SELECT id, session_id, source, suggestion, created_at, task_id "
        "FROM suggestions WHERE " + " AND ".join(where) +
        " ORDER BY created_at DESC"
    )
    rows = db.query(sql, tuple(params))
    if not rows:
        scope = "any" if show_all else "open"
        print(f"No {scope} suggestions.")
        return
    for r in rows:
        state = "OPEN" if r["task_id"] is None else f"→ E-{r['task_id']}"
        print(f"#{r['id']:>4}  [{r['source']:<22}] {state:<10} {r['suggestion']}")


def show_suggestion(suggestion_id: int) -> None:
    row = db.get_db().execute(
        "SELECT id, session_id, project_id, source, trigger_ctx, suggestion, "
        "created_at, task_id, notes FROM suggestions WHERE id = ?",
        (suggestion_id,),
    ).fetchone()
    if row is None:
        print(f"Suggestion #{suggestion_id} not found.")
        return
    state = "open" if row["task_id"] is None else f"accepted into E-{row['task_id']}"
    print(f"Suggestion #{row['id']} ({state})")
    print(f"  source:     {row['source']}")
    print(f"  session:    {row['session_id']}")
    print(f"  created:    {row['created_at']}")
    if row["trigger_ctx"]:
        print(f"  context:    {row['trigger_ctx']}")
    print(f"  suggestion: {row['suggestion']}")
    if row["notes"]:
        print(f"  notes:      {row['notes']}")


def accept_suggestion(
    suggestion_id: int,
    task_type: str,
    parent: int | None,
    project: str | None,
) -> None:
    """Accept a suggestion: create a task from its body and link the suggestion to it."""
    row = db.get_db().execute(
        "SELECT project_id, source, suggestion, task_id FROM suggestions WHERE id = ?",
        (suggestion_id,),
    ).fetchone()
    if row is None:
        print(f"Suggestion #{suggestion_id} not found.")
        return
    if row["task_id"] is not None:
        print(f"Suggestion #{suggestion_id} already accepted into E-{row['task_id']}.")
        return

    project_id = row["project_id"]
    if project_id is None:
        project_id, _ = _resolve_project(project)
    title = f"Address suggestion: {row['suggestion']}"
    description = (
        f"Accepted from suggestion #{suggestion_id} (source={row['source']}).\n\n"
        f"Suggestion text: {row['suggestion']}"
    )

    cursor = db.execute(
        "INSERT INTO tasks (project_id, parent_id, title, description, type, status, phase) "
        "VALUES (?, ?, ?, ?, ?, 'needs_plan', 'now')",
        (project_id, parent, title[:200], description, task_type),
    )
    new_task_id = cursor.lastrowid
    db.execute(
        "UPDATE suggestions SET task_id = ? WHERE id = ?",
        (new_task_id, suggestion_id),
    )
    print(f"Accepted suggestion #{suggestion_id} → E-{new_task_id} ({task_type}).")
