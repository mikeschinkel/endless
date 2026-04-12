"""Unregister and purge command logic."""

import shutil
from pathlib import Path

import click

from endless import db, config


def unregister_project(name: str):
    """Set project status to unregistered and remove from DB.

    Keeps .endless/config.json on disk (with status=unregistered)
    so the project's metadata is preserved.
    Does NOT add to ignore list — discover can offer to re-register.
    """
    row = db.query(
        "SELECT id, name, path FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )

    project_path = Path(row[0]["path"])
    project_id = row[0]["id"]

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

    # Remove from DB
    db.execute(
        "DELETE FROM documents WHERE project_id = ?",
        (project_id,),
    )
    db.execute(
        "DELETE FROM notes WHERE project_id = ?",
        (project_id,),
    )
    db.execute(
        "DELETE FROM project_deps "
        "WHERE project_id = ? OR depends_on_id = ?",
        (project_id, project_id),
    )
    db.execute(
        "DELETE FROM projects WHERE id = ?",
        (project_id,),
    )

    click.echo(
        click.style("•", fg="cyan")
        + f" Unregistered {click.style(name, bold=True)}"
        + " (config preserved on disk)"
    )


def purge_project(name: str):
    """Delete .endless/ directory entirely and add to ignore list.

    This is the nuclear option — removes all Endless metadata
    and prevents discover from suggesting re-registration.
    """
    row = db.query(
        "SELECT id, name, path FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )

    project_path = Path(row[0]["path"])
    project_id = row[0]["id"]

    # Confirm
    click.confirm(
        f"This will delete {project_path / '.endless'} "
        f"and add to ignore list. Continue?",
        abort=True,
    )

    # Remove from DB
    db.execute(
        "DELETE FROM documents WHERE project_id = ?",
        (project_id,),
    )
    db.execute(
        "DELETE FROM notes WHERE project_id = ?",
        (project_id,),
    )
    db.execute(
        "DELETE FROM project_deps "
        "WHERE project_id = ? OR depends_on_id = ?",
        (project_id, project_id),
    )
    db.execute(
        "DELETE FROM projects WHERE id = ?",
        (project_id,),
    )

    # Delete .endless directory
    endless_dir = project_path / ".endless"
    if endless_dir.is_dir():
        shutil.rmtree(endless_dir)
        click.echo(
            click.style("•", fg="cyan")
            + f" Removed {click.style(str(endless_dir), dim=True)}"
        )

    # Add to ignore list
    config.add_ignore(project_path)

    click.echo(
        click.style("•", fg="cyan")
        + f" Purged {click.style(name, bold=True)}"
        + " (added to ignore list)"
    )
