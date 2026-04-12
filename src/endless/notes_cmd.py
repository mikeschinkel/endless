"""Notes command logic — view and manage notes for a project."""

from datetime import datetime, timezone
from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config


def _resolve_project(name: str | None) -> tuple[int, str]:
    """Resolve project name, return (id, name)."""
    if not name:
        cwd = Path.cwd()
        pcfg = config.project_config_read(cwd)
        if pcfg:
            name = pcfg.get("name")
        if not name:
            row = db.query(
                "SELECT name FROM projects WHERE path = ?",
                (str(cwd),),
            )
            if row:
                name = row[0]["name"]
        if not name:
            raise click.ClickException(
                "Not in a registered project directory. "
                "Specify a name: endless notes <name>"
            )

    row = db.query(
        "SELECT id, name FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )
    return row[0]["id"], row[0]["name"]


def _truncate(text: str, max_len: int = 60) -> str:
    if len(text) <= max_len:
        return text
    return text[: max_len - 3] + "..."


def list_notes(name: str | None = None, show_all: bool = False):
    project_id, project_name = _resolve_project(name)

    where = "WHERE n.project_id = ?"
    params: list = [project_id]
    if not show_all:
        where += " AND n.resolved = 0"

    rows = db.query(
        f"SELECT n.id, n.note_type, n.message, "
        f"n.created_at, n.resolved, n.resolved_at "
        f"FROM notes n {where} "
        f"ORDER BY n.created_at DESC",
        tuple(params),
    )

    if not rows:
        if show_all:
            click.echo(
                click.style("•", fg="cyan")
                + f" No notes for "
                + click.style(project_name, bold=True)
            )
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No pending notes for "
                + click.style(project_name, bold=True)
            )
        return

    table_rows = []
    for row in rows:
        created = row["created_at"] or ""
        if "T" in created:
            created = created.split("T")[0]

        cols = [
            row["id"],
            row["note_type"],
            _truncate(row["message"]),
            created,
        ]

        if show_all:
            if row["resolved"]:
                resolved_at = row["resolved_at"] or ""
                if "T" in resolved_at:
                    resolved_at = resolved_at.split("T")[0]
                cols.append(click.style(resolved_at, fg="green"))
            else:
                cols.append(click.style("pending", fg="yellow"))

        table_rows.append(cols)

    headers = ["ID", "TYPE", "MESSAGE", "CREATED"]
    if show_all:
        headers.append("RESOLVED")

    pending = sum(1 for r in rows if not r["resolved"])
    total = len(rows)

    click.echo()
    click.echo(tabulate(
        table_rows,
        headers=headers,
        tablefmt="simple",
        disable_numparse=True,
    ))
    click.echo()
    if show_all:
        click.echo(click.style(
            f"{total} note(s), {pending} pending", dim=True,
        ))
    else:
        click.echo(click.style(
            f"{pending} pending note(s). "
            "Use 'endless resolve <id>' to resolve.",
            dim=True,
        ))


def add_note(name: str | None, message: str):
    project_id, project_name = _resolve_project(name)

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    cursor = db.execute(
        "INSERT INTO notes "
        "(project_id, note_type, message, created_at) "
        "VALUES (?, 'general', ?, ?)",
        (project_id, message, now),
    )
    note_id = cursor.lastrowid

    click.echo(
        click.style("•", fg="cyan")
        + f" Added note #{note_id} to "
        + click.style(project_name, bold=True)
    )


def resolve_note(note_id: int):
    row = db.query(
        "SELECT id, resolved FROM notes WHERE id = ?",
        (note_id,),
    )
    if not row:
        raise click.ClickException(f"No note found with id {note_id}")

    if row[0]["resolved"]:
        click.echo(
            click.style("•", fg="cyan")
            + f" Note #{note_id} is already resolved"
        )
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE notes SET resolved = 1, resolved_at = ? WHERE id = ?",
        (now, note_id),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Resolved note #{note_id}"
    )
