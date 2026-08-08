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


# Canonical endless entries every registered project's .gitignore should carry.
# Only paths endless writes that must never be committed and whose currency is
# settled. `.endless/tmp/` is the sanctioned project-local scratch dir — agents
# author throwaway content there (co-located with the work, survives reboot,
# recoverable before a worktree drops) instead of system /tmp. `worktree.json`
# (write-once identity) and `worktree.lock` (per-session ownership) are per-
# worktree state that must never be committed — kept per ED-1530. NOT
# `.endless/sessions/`: that companion-file path was pruned, so new projects must
# never scaffold it.
GITIGNORE_ENTRIES = [
    ".endless/worktrees/",
    ".endless/tmp/",
    ".endless/worktree.json",
    ".endless/worktree.lock",
]
GITIGNORE_BLOCK_HEADER = "# endless (managed by `endless project init`)"


def scaffold_gitignore(project_path: Path) -> list[str]:
    """Idempotently ensure the project's .gitignore ignores the canonical
    endless paths. Appends only the missing entries under a marked block and
    creates .gitignore if absent; entries already present anywhere in the file
    (in any form) are left as-is. Returns the entries newly added — empty when
    the file already covered them all, so re-running never duplicates a line."""
    gitignore = project_path / ".gitignore"
    existing = gitignore.read_text() if gitignore.exists() else ""
    present = {line.strip() for line in existing.splitlines()}
    missing = [entry for entry in GITIGNORE_ENTRIES if entry not in present]
    if not missing:
        return []
    block = GITIGNORE_BLOCK_HEADER + "\n" + "\n".join(missing) + "\n"
    if existing and not existing.endswith("\n"):
        existing += "\n"
    separator = "\n" if existing else ""
    gitignore.write_text(existing + separator + block)
    return missing


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

    # Write .endless/config.json — merge over any existing config so re-running
    # never clobbers keys endless doesn't manage here (e.g. self_dev, matchers)
    # and preserves an existing project's dependencies/documents.
    cfg = config.project_config_read(project_path) or {}
    cfg.update({
        "name": name,
        "label": label,
        "description": description,
        "language": language,
        "status": status,
    })
    cfg.setdefault("dependencies", [])
    cfg.setdefault("documents", {"rules": []})
    config.project_config_write(project_path, cfg)

    # Scaffold .gitignore + the project-local scratch dir so agents author
    # throwaway content under .endless/tmp/ instead of system /tmp, and never
    # hit a missing-directory trap when they do.
    added = scaffold_gitignore(project_path)
    (project_path / ".endless" / "tmp").mkdir(parents=True, exist_ok=True)

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
    if added:
        click.echo(
            click.style("•", fg="cyan")
            + " .gitignore updated with "
            + click.style(", ".join(added), dim=True)
        )

    _scaffold_output_style(project_path)

    return name


def _scaffold_output_style(project_path: Path) -> None:
    """Place (never activate) the Endless output style during registration.

    Registration scaffolds the file so a newly-registered project has it on
    disk; it deliberately does NOT select it. Activation changes how every
    session in the project answers, which is too large a behavioral change to
    fall out of `project init` — the user opts in with
    `endless setup output-style --activate` or `/config output-style=Endless`.

    Best-effort: registration must not fail because the style could not be
    written (E-1919). A failure is reported, not raised.
    """
    from endless.setup import setup_output_style

    try:
        setup_output_style(cwd=project_path)
    except click.ClickException as e:
        click.echo(
            click.style("•", fg="yellow")
            + " Output style not scaffolded: "
            + click.style(e.format_message(), dim=True)
        )
