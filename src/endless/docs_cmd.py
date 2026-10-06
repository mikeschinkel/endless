"""Docs command logic — list tracked documents for a project."""

from pathlib import Path

import click
from tabulate import tabulate

from endless import agent_help, db, provenance
from endless.doc_types import DOC_TYPE_NAMES
from endless.project_path import project_name_for_cwd


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
        name = project_name_for_cwd(cwd)
        if not name:
            raise agent_help.no_report(
                "The current directory is in no registered project, so "
                "`endless docs` has nothing to list.",
                "Name the project explicitly: endless docs <name>",
                text=("Not in a registered project directory. "
                      "Specify a name: endless docs <name>"),
            )

    row = db.query(
        "SELECT id, name, path FROM live_projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise agent_help.no_report(
            f"No registered project is named '{name}'.",
            "Find the registered name with `endless project list` and retry",
            text=f"No project found with name '{name}'",
        )
    # E-1668: record what this invocation resolved, so the provenance trace can
    # say so when it is not the project enclosing cwd.
    provenance.record_project(row[0]["name"])
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
            raise agent_help.no_report(
                f"'{type_filter}' is not a document type Endless tracks.",
                f"Retry with one of {', '.join(sorted(DOC_TYPE_NAMES))}",
                text=(f"Unknown doc type '{type_filter}'. "
                      f"Valid types: {', '.join(sorted(DOC_TYPE_NAMES))}"),
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
            + ". Run " + click.style("endless project scan", bold=True)
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
