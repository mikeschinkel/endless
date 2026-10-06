"""Unregister and purge command logic."""

import shutil
from pathlib import Path

import click

from endless import agent_help, db, config
from endless.event_bridge import project_registry
from endless.project_path import resolved


def unregister_project(name: str):
    """Mark the project's directory ignored (E-2251).

    The row stays — with its name, tasks, notes and dependencies — and only its
    status changes, so nothing that references it dangles and `project
    register` brings it back whole. Keeps .endless/config.json on disk (with
    status=unregistered) so the project's metadata is preserved.
    """
    row = db.query(
        "SELECT id, name, path FROM live_projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise agent_help.no_report(
            f"No registered project is named '{name}'. "
            "Nothing was unregistered.",
            "Find the registered name with `endless project list` and retry",
            text=f"No project found with name '{name}'",
        )

    project_path = resolved(row[0]["path"])

    # Update config on disk to status=unregistered
    cfg = config.project_config_read(project_path)
    if cfg:
        cfg["status"] = "unregistered"
        config.project_config_write(project_path, cfg)
        click.echo(
            click.style("•", fg="cyan")
            + f" Set status to 'unregistered' in "
            + click.style(
                str(project_path / ".endless" / "config.json"),
                dim=True,
            )
        )

    project_registry("ignore", str(project_path))

    click.echo(
        click.style("•", fg="cyan")
        + f" Unregistered {click.style(name, bold=True)}"
        + " (now ignored; config preserved on disk — "
        + "`endless project register` brings it back)"
    )


def purge_project(name: str):
    """Delete .endless/ directory entirely and mark the directory ignored.

    This is the nuclear option — removes all Endless metadata on disk and
    prevents every registration path from picking it up again. The projects
    row stays, as ignored, so tasks recorded against it keep their project.
    """
    row = db.query(
        "SELECT id, name, path FROM live_projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise agent_help.no_report(
            f"No registered project is named '{name}'. Nothing was purged.",
            "Find the registered name with `endless project list` and retry",
            text=f"No project found with name '{name}'",
        )

    project_path = resolved(row[0]["path"])

    # Confirm
    click.confirm(
        f"This will delete {project_path / '.endless'} "
        f"and mark the directory ignored. Continue?",
        abort=True,
    )

    project_registry("ignore", str(project_path))

    # Delete .endless directory
    endless_dir = project_path / ".endless"
    if endless_dir.is_dir():
        shutil.rmtree(endless_dir)
        click.echo(
            click.style("•", fg="cyan")
            + f" Removed {click.style(str(endless_dir), dim=True)}"
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Purged {click.style(name, bold=True)}"
        + " (directory now ignored)"
    )
