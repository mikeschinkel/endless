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


def forget_project(name_or_path: str, with_tasks: bool = False):
    """Make Endless stop knowing a mistaken registration (E-2349).

    Unlike unregister, nothing is written to the project's own files and no
    ignored row is left: the row is tombstoned by `endless-go project forget`,
    which frees its path and name for a later registration and keeps what it
    held in projects.forgotten_from. An ignored row can be forgotten too, which
    is how an earlier unregister of a mistake is cleaned up.

    A project with tasks is refused unless `with_tasks`, which first removes
    them through task.deleted events tagged with this project, so the removal
    lands in its own ledger beside the tasks it removes. Relations block that,
    as they block `task remove` (E-1915): they are unlinked first, never cut.
    """
    from endless.event_bridge import emit_event
    from endless.task_cmd import _orphan_refusal, _relations_referencing

    rows = db.query(
        "SELECT id, name, path FROM projects WHERE status != 'forgotten' "
        "AND (name = ? OR path = ?)",
        (name_or_path, _stored_or_none(name_or_path)),
    )
    if not rows:
        raise agent_help.no_report(
            f"No registered or ignored project is named or located at "
            f"'{name_or_path}'. Nothing was forgotten.",
            "Find it with `endless project list` or `endless project ignore` "
            "and retry",
            text=f"No project found for '{name_or_path}'",
        )
    row = rows[0]
    project_path = resolved(row["path"])
    tasks = db.query(
        "SELECT id, COALESCE(title, description) AS title FROM live_tasks "
        "WHERE project_id = ? ORDER BY id",
        (row["id"],),
    )
    task_ids = [t["id"] for t in tasks]
    if task_ids and not with_tasks:
        raise agent_help.report(
            f"'{row['name']}' has {len(task_ids)} task(s), and forgetting it "
            "would leave them belonging to nothing. Nothing was forgotten.",
            "whether to remove its tasks with `endless project forget "
            f"{row['name']} --with-tasks`, or keep them and hide the project "
            f"with `endless project ignore {project_path}`",
            text=(f"'{row['name']}' has {len(task_ids)} task(s). Remove them "
                  f"too with `endless project forget {row['name']} "
                  "--with-tasks`, or keep them and hide the project with "
                  f"`endless project ignore {project_path}`."),
        )
    if task_ids:
        found = _relations_referencing(task_ids)
        if found:
            raise _orphan_refusal(
                f"'{row['name']}''s tasks have {len(found)} relation(s).",
                found,
                group_by_task=True,
            )
        for task in tasks:
            emit_event(
                kind="task.deleted",
                project=row["name"],
                entity_type="task",
                entity_id=str(task["id"]),
                payload={"cascade": False, "title": task["title"]},
                project_root=str(project_path),
            )
        click.echo(
            click.style("•", fg="cyan")
            + f" Removed {len(task_ids)} task(s) of "
            + click.style(row["name"], bold=True)
        )

    result = project_registry("forget", str(project_path))
    held = result.get("forgotten_from", {})
    click.echo(
        click.style("•", fg="cyan")
        + f" Forgot {click.style(row['name'], bold=True)}"
        + " — its path and name can be registered again. It held: "
        + click.style(
            ", ".join(f"{k}={v}" for k, v in held.items() if v), dim=True,
        )
    )


def _stored_or_none(text: str) -> str | None:
    """The stored path form of `text` when it looks like a path, else None."""
    if "/" not in text and text != "~":
        return None
    from endless.project_path import stored
    return stored(text)
