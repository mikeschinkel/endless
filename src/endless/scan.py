"""Scan command logic — reconcile projects and update timestamps."""

from datetime import datetime, timezone
from pathlib import Path

import click

from endless import db


def scan_project(project_id: int, project_name: str, project_path: Path):
    if not project_path.is_dir():
        click.echo(
            click.style("warning:", fg="yellow")
            + f" Project path missing: {project_path} ({project_name})"
        )
        return

    click.echo(
        f"  {click.style(project_name, bold=True)} "
        + click.style(str(project_path), dim=True)
    )

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE projects SET updated_at=? WHERE id=?",
        (now, project_id),
    )


def run_scan(project_name: str | None = None, docs_only: bool = False):
    from endless.reconcile import reconcile
    reconcile()

    click.echo(click.style("•", fg="cyan") + " Scanning projects...")
    click.echo()

    projects_scanned = 0

    if project_name:
        rows = db.query(
            "SELECT id, name, path FROM projects WHERE name=?",
            (project_name,),
        )
        if not rows:
            raise click.ClickException(
                f"No project found with name '{project_name}'"
            )
    else:
        rows = db.query(
            "SELECT id, name, path FROM projects "
            "WHERE status != 'archived' ORDER BY name"
        )

    if not rows:
        click.echo(click.style("•", fg="cyan") + " No projects to scan.")
        return

    for row in rows:
        scan_project(row["id"], row["name"], Path(row["path"]))
        projects_scanned += 1

    click.echo()
    click.echo(
        click.style("•", fg="cyan")
        + f" Scan complete: {projects_scanned} project(s)"
    )
