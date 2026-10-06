"""`endless project ignore` / `unignore` — directories that are not projects.

A directory is ignored by a projects row with status `ignored`, or by an
`.endless-ignore` marker file in it (E-2251). Either covers its subtree, and the
nearest registered-or-ignored ancestor wins: ignoring ~/Projects keeps every
new directory under it from being auto-registered, while a project registered
inside it stays a project. Explicit `endless project register` still works
under an ignored directory.
"""

from pathlib import Path

import click

from endless import agent_help, config
from endless.event_bridge import project_registry
from endless.project_path import resolved, stored


def _bullet(text: str) -> None:
    click.echo(click.style("•", fg="cyan") + " " + text)


def list_ignored() -> None:
    rows = project_registry("list-ignored")
    if not rows:
        click.echo("No directories are ignored.")
        return
    for r in rows:
        note = ""
        if r["name"] != r["path"]:
            note = f"  (was project {r['name']}"
            note += f", {r['tasks']} task(s))" if r["tasks"] else ")"
        click.echo(r["path"] + click.style(note, dim=True))


def ignore(path: Path, marker: bool) -> None:
    target = resolved(path)
    if not target.is_dir():
        raise agent_help.no_report(
            f"{target} is not a directory. Nothing was ignored.",
            "Correct the path and retry",
            text=f"Directory not found: {target}",
        )
    result = project_registry("ignore", str(target))
    if result.get("existed"):
        _bullet(f"Ignored {click.style(result['path'], bold=True)} "
                "(its existing row is kept, now marked ignored)")
    else:
        _bullet(f"Ignored {click.style(result['path'], bold=True)}")
    if marker:
        for written in config.write_ignore_marker(target):
            _bullet("Wrote " + click.style(written, dim=True))


def unignore(path: Path) -> None:
    target = resolved(path)
    marker = target / config.IGNORE_MARKER
    removed_marker = False
    if marker.is_file():
        marker.unlink()
        removed_marker = True
        _bullet("Removed " + click.style(str(marker), dim=True))

    verdict = project_registry("resolve", str(target))[0]
    if verdict["kind"] != config.IGNORED_STATUS:
        if not removed_marker:
            raise agent_help.no_report(
                f"{target} is not ignored. Nothing was changed.",
                "Check the list with `endless project ignore` and retry",
                text=f"Not ignored: {target}",
            )
        return
    if verdict["at"] != stored(target) or verdict.get("marker"):
        raise agent_help.no_report(
            f"{target} is ignored because {verdict['at']} is"
            + (" (by its marker file)" if verdict.get("marker") else "")
            + ". Nothing more was changed.",
            "Register this directory with `endless project register` (allowed "
            f"under an ignored one), or unignore {verdict['at']} itself",
            text=f"Ignored by an ancestor: {verdict['at']}",
        )
    project_registry("clear", str(target))
    _bullet(f"No longer ignoring {click.style(stored(target), bold=True)}")
