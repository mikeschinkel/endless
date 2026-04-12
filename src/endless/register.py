"""Register command logic."""

import re
from datetime import datetime, timezone
from pathlib import Path

import click

from endless import db, config

NAME_PATTERN = re.compile(r"^[a-z0-9][a-z0-9_-]*$")

LANGUAGE_EXTENSIONS = {
    ".go": "go",
    ".ts": "typescript",
    ".tsx": "typescript",
    ".js": "javascript",
    ".jsx": "javascript",
    ".py": "python",
    ".rs": "rust",
    ".rb": "ruby",
    ".sh": "bash",
    ".bash": "bash",
}


def validate_name(name: str) -> bool:
    return bool(NAME_PATTERN.match(name))


def detect_language(project_path: Path) -> str:
    counts: dict[str, int] = {}
    for f in project_path.rglob("*"):
        if f.is_file() and len(f.relative_to(project_path).parts) <= 2:
            lang = LANGUAGE_EXTENSIONS.get(f.suffix)
            if lang:
                counts[lang] = counts.get(lang, 0) + 1
    if not counts:
        return ""
    return max(counts, key=counts.get)


def register_project(
    project_path: Path,
    name: str | None = None,
    label: str | None = None,
    description: str | None = None,
    language: str | None = None,
    status: str | None = None,
    infer: bool = False,
) -> str:
    """Register or update a project. Returns the name."""

    project_path = project_path.resolve()
    if not project_path.is_dir():
        raise click.ClickException(
            f"Directory not found: {project_path}"
        )

    # Check if already registered
    existing = db.query(
        "SELECT id, name, label, description, language, status "
        "FROM projects WHERE path = ?",
        (str(project_path),),
    )
    is_update = len(existing) > 0

    if is_update:
        row = existing[0]
        click.echo(
            click.style("•", fg="cyan")
            + f" Project already registered at "
            + click.style(str(project_path), bold=True)
            + " — updating"
        )

    dir_name = project_path.name
    detected_lang = detect_language(project_path)

    # Determine defaults from existing or inference
    if is_update:
        row = existing[0]
        default_name = row["name"]
        default_label = row["label"] or ""
        default_desc = row["description"] or ""
        default_lang = row["language"] or detected_lang
        default_status = row["status"]
    else:
        default_name = dir_name
        default_label = ""
        default_desc = ""
        default_lang = detected_lang
        default_status = "active"

    # Resolve values: explicit flag > interactive prompt > default
    if infer:
        name = name or default_name
        label = label if label is not None else default_label
        description = description if description is not None else default_desc
        language = language or default_lang
        status = status or default_status
    else:
        if name is None:
            name = click.prompt(
                click.style("Name (identifier)", bold=True),
                default=default_name,
            )
        if label is None:
            label = click.prompt(
                click.style("Label (display)", bold=True),
                default=default_label or "",
            )
        if description is None:
            description = click.prompt(
                click.style("Description", bold=True),
                default=default_desc or "",
            )
        if language is None:
            language = click.prompt(
                click.style("Language", bold=True),
                default=default_lang or "",
            )
        if status is None:
            status = click.prompt(
                click.style("Status", bold=True),
                default=default_status,
                type=click.Choice(
                    ["active", "paused", "archived", "idea"]
                ),
            )

    # Validate
    if not validate_name(name):
        raise click.ClickException(
            f"Invalid name: '{name}' "
            "(must be lowercase alphanumeric, hyphens, or underscores)"
        )
    if status not in ("active", "paused", "archived", "idea"):
        raise click.ClickException(f"Invalid status: {status}")

    # Write .endless/config.json
    config.project_config_write(project_path, {
        "name": name,
        "label": label,
        "description": description,
        "language": language,
        "status": status,
        "dependencies": [],
        "documents": {"rules": []},
    })

    # Detect group from parent directory
    group_name = None
    parent = project_path.parent
    roots = config.get_roots()
    if parent not in roots:
        group_name = parent.name

    # Upsert into database
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    if is_update:
        db.execute(
            "UPDATE projects SET name=?, label=?, group_name=?, "
            "description=?, status=?, language=?, updated_at=? "
            "WHERE path=?",
            (name, label, group_name, description, status,
             language, now, str(project_path)),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Updated {click.style(name, bold=True)}"
        )
    else:
        db.execute(
            "INSERT INTO projects "
            "(name, label, path, group_name, description, "
            "status, language, created_at, updated_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (name, label, str(project_path), group_name,
             description, status, language, now, now),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Registered {click.style(name, bold=True)}"
            + f" at {project_path}"
        )

    click.echo(
        click.style("•", fg="cyan")
        + " Config written to "
        + click.style(
            str(project_path / ".endless" / "config.json"),
            dim=True,
        )
    )
    return name
