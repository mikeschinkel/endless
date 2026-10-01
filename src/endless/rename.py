"""Rename command logic — change a project's name."""

from pathlib import Path

import click

from endless import agent_help, db, config
from endless.register import validate_name
from endless.project_path import resolved
from endless.resolve_name import resolve_project


def rename_project(
    old_name: str, new_name: str, path_hint: str | None = None,
):
    if not validate_name(new_name):
        raise agent_help.no_report(
            f"'{new_name}' is not a valid project name. Nothing was renamed.",
            "Retry with a lowercase alphanumeric name (hyphens and "
            "underscores allowed)",
            text=(f"Invalid name: '{new_name}' "
                  "(must be lowercase alphanumeric, hyphens, "
                  "or underscores)"),
        )

    project = resolve_project(old_name, path_hint)

    # Check new name isn't taken
    if db.exists(
        "SELECT 1 FROM projects WHERE name = ?",
        (new_name,),
    ):
        # Who picked the new name decides the class, and this function cannot
        # see that: the same call arrives whether the user dictated the name or
        # the agent invented one. Both branches are named instead (ED, 2026-09-18).
        raise agent_help.report_if(
            f"'{new_name}' is already the name of another project. "
            f"'{old_name}' was not renamed.",
            "the user chose the new name",
            "retry with an unused name",
            "another name, or renaming the project that holds this one, is "
            "theirs to pick",
            text=f"Name '{new_name}' is already in use",
        )

    project_path = resolved(project["path"])

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
