"""List command logic."""

from pathlib import Path

import click
from tabulate import tabulate

from endless import db

STATUS_COLORS = {
    "active": "green",
    "paused": "yellow",
    "archived": None,
    "idea": "blue",
}


def list_projects(status_filter: str | None = None, group: bool = False):
    from endless.reconcile import reconcile
    reconcile()

    where = ""
    params = ()
    if status_filter:
        where = "WHERE p.status = ?"
        params = (status_filter,)

    order = "ORDER BY p.group_name NULLS FIRST, p.name"

    rows = db.query(
        f"SELECT p.name, COALESCE(NULLIF(p.label,''),'') as label, "
        f"p.status, COALESCE(NULLIF(p.language,''),'') as language, "
        f"COALESCE(p.group_name,'') as group_name, p.path, "
        f"(SELECT count(*) FROM notes n "
        f"WHERE n.project_id = p.id AND n.resolved = 0) as pending_notes "
        f"FROM projects p {where} {order}",
        params,
    )

    if not rows:
        if status_filter:
            click.echo(
                click.style("•", fg="cyan")
                + f" No projects with status '{status_filter}'"
            )
        else:
            click.echo(
                click.style("•", fg="cyan")
                + " No projects registered yet. Run "
                + click.style("endless register", bold=True)
                + " to add one."
            )
        return

    home = str(Path.home())
    current_group = None
    table_rows = []

    for row in rows:
        grp = row["group_name"]

        # Group headers (inserted as separator rows)
        if group and grp != current_group:
            if table_rows:
                # Flush current table before group header
                _print_table(table_rows)
                table_rows = []
            if grp:
                click.echo()
                click.echo(click.style(f"[{grp}]", bold=True, fg="cyan"))
            elif current_group:
                click.echo()
                click.echo(click.style("[ungrouped]", bold=True, dim=True))
            current_group = grp

        short_path = row["path"].replace(home, "~")

        status_str = click.style(
            row["status"],
            fg=STATUS_COLORS.get(row["status"]),
            dim=(row["status"] == "archived"),
        )

        notes = row["pending_notes"]
        if notes > 0:
            notes_str = click.style(f"{notes} pending", fg="yellow")
        else:
            notes_str = click.style("-", dim=True)

        table_rows.append([
            row["name"],
            row["label"] or "-",
            status_str,
            row["language"] or "-",
            notes_str,
            click.style(short_path, dim=True),
        ])

    if table_rows:
        _print_table(table_rows)

    click.echo()
    click.echo(click.style(f"{len(rows)} project(s)", dim=True))


def _print_table(rows: list[list]):
    headers = ["NAME", "LABEL", "STATUS", "LANGUAGE", "NOTES", "PATH"]
    click.echo(tabulate(
        rows,
        headers=headers,
        tablefmt="simple",
        disable_numparse=True,
    ))
