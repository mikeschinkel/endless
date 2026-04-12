"""Rename command logic — change a project's name."""

from pathlib import Path

import click

from endless import db, config
from endless.register import validate_name
from endless.resolve_name import resolve_project


def rename_project(
    old_name: str, new_name: str, path_hint: str | None = None,
):
    if not validate_name(new_name):
        raise click.ClickException(
            f"Invalid name: '{new_name}' "
            "(must be lowercase alphanumeric, hyphens, "
            "or underscores)"
        )

    project = resolve_project(old_name, path_hint)

    # Check new name isn't taken
    if db.exists(
        "SELECT 1 FROM projects WHERE name = ?",
        (new_name,),
    ):
        raise click.ClickException(
            f"Name '{new_name}' is already in use"
        )

    project_path = Path(project["path"])

    # Update DB
    db.execute(
        "UPDATE projects SET name=? WHERE id=?",
        (new_name, project["id"]),
    )

    # Update .endless/config.json on disk
    cfg = config.project_config_read(project_path)
    if cfg:
        cfg["name"] = new_name
        config.project_config_write(project_path, cfg)

    click.echo(
        click.style("•", fg="cyan")
        + f" Renamed {click.style(old_name, bold=True)}"
        + f" → {click.style(new_name, bold=True)}"
    )
