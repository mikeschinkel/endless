"""Set command logic — update a project field."""

import re
from pathlib import Path

import click

from endless import agent_help, db, config, settings_export
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
    "content", "dependencies", "documents", "minimizer", "self_dev",
)


def _fields_help() -> str:
    """The inventory both refusals end with: what this command writes, and
    where everything else is edited."""
    return (
        f"  `project set` writes: {', '.join(sorted(SETTABLE_FIELDS))}\n"
        f"  and every registered setting a project file may hold "
        f"(`endless-go settings list` names them), e.g. main_sync.enabled=on\n"
        f"  Other keys are edited directly in the project's "
        f".endless/config.json: {', '.join(FILE_ONLY_FIELDS)}"
    )

# name.field=value (explicit project)
NAMED_PATTERN = re.compile(r"^([a-z0-9][a-z0-9_-]*)\.(\w+)=(.*)$")
# field=value (current directory)
LOCAL_PATTERN = re.compile(r"^(\w+)=(.*)$")
# A dotted key and its value: key=value, or name.key=value (E-2284). Which of
# the two a given expression is depends on what the settings registry holds,
# so the split is decided in _registered_target, not by the pattern.
DOTTED_PATTERN = re.compile(r"^([a-z0-9][a-z0-9_.-]*\.[a-z0-9_]+)=(.*)$")


def set_field(expression: str, path_hint: str | None = None):
    target = _registered_target(expression)
    if target is not None:
        _set_registered(*target, path_hint=path_hint)
        return

    # Try named pattern first
    m = NAMED_PATTERN.match(expression)
    if m:
        name, field, value = m.group(1), m.group(2), m.group(3)
        project = resolve_project(name, path_hint)
    else:
        # Try local pattern (field=value in current dir)
        m = LOCAL_PATTERN.match(expression)
        if not m:
            raise agent_help.no_report(
                f"'{expression}' is not a `project set` expression. "
                "Nothing was changed.",
                "Retry as <field>=<value> from inside the project, or "
                "<name>.<field>=<value> from anywhere",
                text=(
                    "Invalid format. Use:\n"
                    "  endless project set <field>=<value>        "
                    "(in a project directory)\n"
                    "  endless project set <name>.<field>=<value>  "
                    "(from anywhere)\n"
                    + _fields_help()
                ),
            )
        field, value = m.group(1), m.group(2)

        # Detect project from current directory
        cwd = Path.cwd()
        name = project_name_for_cwd(cwd)
        if not name:
            raise agent_help.no_report(
                "The current directory is not in a registered project, so "
                "<field>=<value> has nothing to apply to. Nothing was changed.",
                "Name the project explicitly: "
                "endless project set <name>.<field>=<value>",
                text=("Not in a registered project directory. "
                      "Use: endless project set <name>.<field>=<value>"),
            )
        project = resolve_project(name, path_hint)

    if field not in SETTABLE_FIELDS:
        raise agent_help.no_report(
            f"'{field}' is not a field `project set` writes. "
            "Nothing was changed.",
            f"Retry with one of {', '.join(sorted(SETTABLE_FIELDS))}, or edit "
            "the project's .endless/config.json for the other keys",
            text=f"Unknown field '{field}'.\n" + _fields_help(),
        )

    project_path = resolved(project["path"])
    name = project["name"]

    # Validate specific fields
    if field == "name" and not validate_name(value):
        raise agent_help.no_report(
            f"'{value}' is not a valid project name. Nothing was changed.",
            "Retry with a lowercase alphanumeric name (hyphens and "
            "underscores allowed)",
            text=(f"Invalid name: '{value}' "
                  "(must be lowercase alphanumeric, hyphens, "
                  "or underscores)"),
        )
    if field == "status" and value not in VALID_STATUSES:
        raise agent_help.no_report(
            f"'{value}' is not a project status. Nothing was changed.",
            f"Retry with one of {', '.join(VALID_STATUSES)}",
            text=(f"Invalid status: '{value}' "
                  f"(must be: {', '.join(VALID_STATUSES)})"),
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


def _registered_target(expression: str):
    """(project name or None, setting, value) when `expression` sets a
    registered setting, else None.

    `main_sync.enabled=on` would also match NAMED_PATTERN as project
    `main_sync`, field `enabled`; the registry decides. The whole left side is
    tried as a key first (the current project), then everything after the
    first dot (a named project).
    """
    m = DOTTED_PATTERN.match(expression)
    if not m:
        return None
    lhs, value = m.group(1), m.group(2)
    name, _, rest = lhs.partition(".")
    if name and rest in SETTABLE_FIELDS:
        return None
    registry = settings_export.by_id(settings_export.metadata())
    if lhs in registry:
        return None, registry[lhs], value
    if rest in registry:
        return name, registry[rest], value
    return None


def _set_registered(name: str | None, setting: dict, value: str,
                    path_hint: str | None = None):
    """Write one registered setting into a project's .endless/config.json.

    Only the project file: a machine-only setting is refused with the file it
    does live in. An opt-in is written "on" or "off"; "unset" removes the key,
    since a missing key is unset.
    """
    key = setting["id"]
    if name is None:
        name = project_name_for_cwd(Path.cwd())
        if not name:
            raise agent_help.no_report(
                "The current directory is not in a registered project, so "
                f"{key}=<value> has nothing to apply to. Nothing was changed.",
                f"Name the project explicitly: "
                f"endless project set <name>.{key}=<value>",
            )
    project = resolve_project(name, path_hint)
    project_path = resolved(project["path"])
    doc = settings_export.export(project_path)

    if setting["files"] == "machine":
        raise agent_help.no_report(
            f"'{key}' is a machine-wide setting; it lives in "
            f"{doc['machine_file']}, not in a project's config. "
            "Nothing was changed.",
            f"Edit {key} in {doc['machine_file']} directly",
        )
    parsed = settings_export.parse(setting, value)
    if parsed is None:
        raise agent_help.no_report(
            f"'{value}' is not a value {key} accepts "
            f"({settings_export.accepted(setting)}). Nothing was changed.",
            f"Retry with {settings_export.accepted(setting)}",
        )

    cfg = config.project_config_read(project_path) or {}
    *parents, leaf = key.split(".")
    node = cfg
    for seg in parents:
        node = node.setdefault(seg, {})
    if setting["kind"] == "optin" and parsed == "unset":
        node.pop(leaf, None)
    else:
        node[leaf] = parsed
    config.project_config_write(project_path, cfg)

    click.echo(
        click.style("•", fg="cyan")
        + f" Set {click.style(project['name'], bold=True)}"
        + f".{key} = {value}"
    )
