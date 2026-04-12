"""Status command logic."""

from pathlib import Path

import click

from endless import db, config


def show_status(name: str | None = None):
    from endless.reconcile import reconcile
    reconcile()

    # Auto-detect from current directory if no name given
    if not name:
        cwd = Path.cwd()
        pcfg = config.project_config_read(cwd)
        if pcfg:
            name = pcfg.get("name")
        if not name:
            row = db.query(
                "SELECT name FROM projects WHERE path = ?",
                (str(cwd),),
            )
            if row:
                name = row[0]["name"]
        if not name:
            raise click.ClickException(
                "Not in a registered project directory. "
                "Specify a name: endless status <name>"
            )

    row = db.query(
        "SELECT id, name, label, description, status, language, "
        "group_name, path, created_at, updated_at "
        "FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(f"No project found with name '{name}'")

    p = row[0]
    home = str(Path.home())
    short_path = p["path"].replace(home, "~")

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

    # Documents
    doc_count = db.scalar(
        "SELECT count(*) FROM documents "
        "WHERE project_id = ? AND is_archived = 0",
        (p["id"],),
    )
    click.echo(f"  {'Documents:':<14} {doc_count} tracked")

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
