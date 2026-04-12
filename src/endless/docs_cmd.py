"""Docs command logic — list tracked documents for a project."""

from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config
from endless.doc_types import DOC_TYPE_NAMES


def _human_size(size_bytes: int) -> str:
    if size_bytes < 1024:
        return f"{size_bytes} B"
    if size_bytes < 1024 * 1024:
        return f"{size_bytes / 1024:.1f} KB"
    return f"{size_bytes / (1024 * 1024):.1f} MB"


def _resolve_project(name: str | None) -> tuple[int, str, str]:
    """Resolve project name, return (id, name, path)."""
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
                "Specify a name: endless docs <name>"
            )

    row = db.query(
        "SELECT id, name, path FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )
    return row[0]["id"], row[0]["name"], row[0]["path"]


def list_docs(
    name: str | None = None, type_filter: str | None = None,
):
    project_id, project_name, project_path = _resolve_project(name)
    home = str(Path.home())
    short_path = project_path.replace(home, "~")

    where = "WHERE d.project_id = ? AND d.is_archived = 0"
    params: list = [project_id]
    if type_filter:
        if type_filter not in DOC_TYPE_NAMES:
            raise click.ClickException(
                f"Unknown doc type '{type_filter}'. "
                f"Valid types: {', '.join(sorted(DOC_TYPE_NAMES))}"
            )
        where += " AND d.doc_type = ?"
        params.append(type_filter)

    rows = db.query(
        f"SELECT d.doc_type, d.relative_path, "
        f"d.size_bytes, d.last_modified "
        f"FROM documents d {where} "
        f"ORDER BY d.doc_type, d.relative_path",
        tuple(params),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan")
            + f" No documents tracked for "
            + click.style(project_name, bold=True)
            + ". Run " + click.style("endless scan", bold=True)
            + " first."
        )
        return

    table_rows = []
    for row in rows:
        modified = row["last_modified"] or ""
        if "T" in modified:
            modified = modified.split("T")[0]

        table_rows.append([
            row["doc_type"],
            row["relative_path"],
            _human_size(row["size_bytes"] or 0),
            modified,
        ])

    click.echo()
    click.echo(
        click.style(f"Documents for {project_name}", bold=True)
        + click.style(f" ({short_path})", dim=True)
    )
    click.echo(tabulate(
        table_rows,
        headers=["TYPE", "PATH", "SIZE", "MODIFIED"],
        tablefmt="simple",
        disable_numparse=True,
    ))
    click.echo()
    click.echo(click.style(f"{len(rows)} document(s)", dim=True))
