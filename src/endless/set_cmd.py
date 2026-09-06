"""Set command logic — update a project field."""

import re
from pathlib import Path

import click

from endless import db, config
from endless.register import validate_name
from endless.resolve_name import resolve_project
from endless.models import VALID_STATUSES
from endless.project_path import project_name_for_cwd, resolved

SETTABLE_FIELDS = {
    "name", "label", "description", "language", "status",
}

# Keys that live in the same config file but that this command does not write.
# Named in the refusal because "Settable fields: ..." read as "the config file
# has five fields", which sent a reader looking for a flag that does not exist
# instead of opening the file (E-1934). Not a whitelist and not validated — it
# exists so the error can say where the rest of the config lives.
FILE_ONLY_FIELDS = (
    "content", "dependencies", "documents", "matchers", "minimizer", "self_dev",
)


def _fields_help() -> str:
    """The two-line inventory both refusals end with: what this command writes,
    and where everything else is edited."""
    return (
        f"  `project set` writes: {', '.join(sorted(SETTABLE_FIELDS))}\n"
        f"  Other keys are edited directly in the project's "
        f".endless/config.json: {', '.join(FILE_ONLY_FIELDS)}"
    )

# name.field=value (explicit project)
NAMED_PATTERN = re.compile(r"^([a-z0-9][a-z0-9_-]*)\.(\w+)=(.*)$")
# field=value (current directory)
LOCAL_PATTERN = re.compile(r"^(\w+)=(.*)$")


def set_field(expression: str, path_hint: str | None = None):
    # Try named pattern first
    m = NAMED_PATTERN.match(expression)
    if m:
        name, field, value = m.group(1), m.group(2), m.group(3)
        project = resolve_project(name, path_hint)
    else:
        # Try local pattern (field=value in current dir)
        m = LOCAL_PATTERN.match(expression)
        if not m:
            raise click.ClickException(
                "Invalid format. Use:\n"
                "  endless project set <field>=<value>        "
                "(in a project directory)\n"
                "  endless project set <name>.<field>=<value>  "
                "(from anywhere)\n"
                + _fields_help()
            )
        field, value = m.group(1), m.group(2)

        # Detect project from current directory
        cwd = Path.cwd()
        name = project_name_for_cwd(cwd)
        if not name:
            raise click.ClickException(
                "Not in a registered project directory. "
                "Use: endless project set <name>.<field>=<value>"
            )
        project = resolve_project(name, path_hint)

    if field not in SETTABLE_FIELDS:
        raise click.ClickException(
            f"Unknown field '{field}'.\n" + _fields_help()
        )

    project_path = resolved(project["path"])
    name = project["name"]

    # Validate specific fields
    if field == "name" and not validate_name(value):
        raise click.ClickException(
            f"Invalid name: '{value}' "
            "(must be lowercase alphanumeric, hyphens, "
            "or underscores)"
        )
    if field == "status" and value not in VALID_STATUSES:
        raise click.ClickException(
            f"Invalid status: '{value}' "
            f"(must be: {', '.join(VALID_STATUSES)})"
        )

    # Update DB
    db.execute(
        f"UPDATE projects SET {field}=? WHERE id=?",
        (value, project["id"]),
    )

    # Update .endless/config.json on disk
    cfg = config.project_config_read(project_path)
    if cfg:
        cfg[field] = value
        config.project_config_write(project_path, cfg)

    click.echo(
        click.style("•", fg="cyan")
        + f" Set {click.style(name, bold=True)}"
        + f".{field} = {value}"
    )
