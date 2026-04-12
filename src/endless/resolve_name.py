"""Resolve a name to a project, handling duplicates with --path."""

import click

from endless import db


def resolve_project(name: str, path_hint: str | None = None) -> dict:
    """Look up a project by name. If duplicates exist, require --path.

    Args:
        name: The project name to look up
        path_hint: Optional path substring to disambiguate

    Returns:
        A sqlite3.Row dict for the matched project

    Raises:
        click.ClickException if not found or ambiguous
    """
    rows = db.query(
        "SELECT id, name, label, path, group_name, description, "
        "status, language, created_at, updated_at "
        "FROM projects WHERE name = ?",
        (name,),
    )

    if not rows:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )

    if len(rows) == 1:
        return dict(rows[0])

    # Multiple matches — need disambiguation
    if not path_hint:
        paths = [r["path"] for r in rows]
        path_list = "\n  ".join(paths)
        raise click.ClickException(
            f"Multiple projects with name '{name}':\n"
            f"  {path_list}\n"
            f"Use --path=<segment> to disambiguate "
            f"(e.g., --path={_suggest_segment(paths)})"
        )

    # Filter by path hint
    matches = [
        r for r in rows
        if path_hint in r["path"]
    ]

    if len(matches) == 0:
        raise click.ClickException(
            f"No project with name '{name}' "
            f"matching path '{path_hint}'"
        )
    if len(matches) > 1:
        paths = [r["path"] for r in matches]
        path_list = "\n  ".join(paths)
        raise click.ClickException(
            f"Path hint '{path_hint}' still matches "
            f"multiple projects:\n  {path_list}\n"
            f"Use a more specific --path segment"
        )

    return dict(matches[0])


def _suggest_segment(paths: list[str]) -> str:
    """Suggest a disambiguating path segment from a list of paths."""
    # Find the first differing path component
    parts_list = [p.split("/") for p in paths]
    for i in range(min(len(p) for p in parts_list)):
        segments = set(p[i] for p in parts_list)
        if len(segments) > 1:
            return sorted(segments)[0]
    return paths[0].split("/")[-2]
