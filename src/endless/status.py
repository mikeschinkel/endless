"""Status command logic."""

from pathlib import Path

import click

from endless import agent_help, db
from endless.project_path import project_name_for_cwd, stored


def show_status(name: str | None = None):
    from endless.reconcile import reconcile
    reconcile()

    # Auto-detect from current directory if no name given
    if not name:
        cwd = Path.cwd()
        name = project_name_for_cwd(cwd)
        if not name:
            # Two different situations reach here and they need opposite
            # answers: a caller standing in the wrong directory only has to
            # name the project, while a project that was never registered is a
            # registration the user has to want. Nothing in cwd distinguishes
            # them — the absence of a row looks identical either way — so both
            # branches are named and the agent, which holds the conversation,
            # picks.
            raise agent_help.report_if(
                "The current directory is in no registered project and no "
                "name was given, so there is nothing to show.",
                "this directory's project is not registered at all",
                "retry with its registered name from `endless project list`",
                "registering a project is theirs to decide",
                text=("Not in a registered project directory. "
                      "Specify a name: endless project info <name>"),
            )

    row = db.query(
        "SELECT id, name, label, description, status, language, "
        "group_name, path, created_at, updated_at "
        "FROM live_projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise agent_help.no_report(
            f"No registered project is named '{name}'.",
            "Look up the registered name with `endless project list` and retry",
            text=f"No project found with name '{name}'",
        )

    p = row[0]
    # Display the STORED form, so a row still written absolute renders like
    # every other one (E-2011).
    short_path = stored(p["path"])

    # Header
    click.echo()
    line = click.style(p["name"], bold=True)
    if p["label"]:
        line += "  " + click.style(f"({p['label']})", dim=True)
    click.echo(line)
    if p["description"]:
        click.echo(click.style(p["description"], dim=True))
    click.echo()

    # Status with color
    status_colors = {
        "active": "green",
        "paused": "yellow",
        "archived": None,
        "idea": "blue",
    }
    status_str = click.style(
        p["status"],
        fg=status_colors.get(p["status"]),
        dim=(p["status"] == "archived"),
    )

    click.echo(f"  {'Label:':<14} {p['label'] or '-'}")
    click.echo(f"  {'Description:':<14} {p['description'] or '-'}")
    click.echo(f"  {'Status:':<14} {status_str}")
    click.echo(f"  {'Language:':<14} {p['language'] or '-'}")
    if p["group_name"]:
        click.echo(f"  {'Group:':<14} {p['group_name']}")
    click.echo(f"  {'Path:':<14} {short_path}")
    click.echo(f"  {'Registered:':<14} {p['created_at']}")
    click.echo(f"  {'Updated:':<14} {p['updated_at']}")

    # Notes
    notes_count = db.scalar(
        "SELECT count(*) FROM notes "
        "WHERE project_id = ? AND resolved = 0",
        (p["id"],),
    )
    if notes_count > 0:
        click.echo(
            f"  {'Notes:':<14} "
            + click.style(f"{notes_count} pending", fg="yellow")
        )
    else:
        click.echo(f"  {'Notes:':<14} " + click.style("none", dim=True))

    # Dependencies
    deps = db.query(
        "SELECT p2.name, pd.dep_type "
        "FROM project_deps pd "
        "JOIN projects p2 ON pd.depends_on_id = p2.id "
        "WHERE pd.project_id = ?",
        (p["id"],),
    )
    if deps:
        click.echo()
        click.echo(click.style("  Dependencies:", bold=True))
        for d in deps:
            click.echo(
                f"    {d['name']} "
                + click.style(f"({d['dep_type']})", dim=True)
            )

    # Dependents
    dependents = db.query(
        "SELECT p2.name, pd.dep_type "
        "FROM project_deps pd "
        "JOIN projects p2 ON pd.project_id = p2.id "
        "WHERE pd.depends_on_id = ?",
        (p["id"],),
    )
    if dependents:
        click.echo()
        click.echo(click.style("  Depended on by:", bold=True))
        for d in dependents:
            click.echo(
                f"    {d['name']} "
                + click.style(f"({d['dep_type']})", dim=True)
            )

    click.echo()
