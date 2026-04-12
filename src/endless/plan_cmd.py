"""Plan command logic — import, show, and manage plan items."""

import re
from datetime import datetime, timezone
from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config


def _resolve_project(name: str | None) -> tuple[int, str]:
    """Resolve project name, return (id, name)."""
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
                "Specify a name: endless plan <command> "
                "--project <name>"
            )
    row = db.query(
        "SELECT id, name FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(
            f"No project found with name '{name}'"
        )
    return row[0]["id"], row[0]["name"]


def _parse_plan_markdown(content: str) -> list[dict]:
    """Parse a markdown plan file into structured items.

    Extracts items from:
    - Bullet lists (- item or * item)
    - Numbered lists (1. item)
    - ## Section headings become phase names

    Returns list of {phase, task_text, sort_order}
    """
    items = []
    current_phase = "now"
    sort_order = 0

    # Map common section names to phases
    phase_map = {
        "now": "now",
        "current": "now",
        "in progress": "now",
        "active": "now",
        "next": "next",
        "upcoming": "next",
        "queued": "next",
        "later": "later",
        "future": "later",
        "deferred": "later",
        "backlog": "later",
        "blocked": "blocked",
        "done": "completed",
        "completed": "completed",
        "context": "_skip",
        "deliverables": "now",
        "verification": "_skip",
    }

    for line in content.splitlines():
        line = line.rstrip()

        # Detect section headings
        heading_match = re.match(r"^#{1,3}\s+(.+)$", line)
        if heading_match:
            heading_text = heading_match.group(1).strip()
            heading_lower = heading_text.lower()
            # Strip common prefixes
            heading_lower = re.sub(
                r"^(phase \d+|step \d+)\s*[—–:-]\s*", "",
                heading_lower,
            )
            # Match to a known phase
            matched = False
            for key, phase in phase_map.items():
                if key in heading_lower:
                    current_phase = phase
                    matched = True
                    break
            if not matched:
                # Unknown heading becomes a "now" phase
                # with the heading as context
                current_phase = "now"
            continue

        if current_phase == "_skip":
            continue

        # Detect list items
        item_match = re.match(
            r"^\s*[-*]\s+(.+)$|^\s*\d+[.)]\s+(.+)$", line
        )
        if item_match:
            text = (item_match.group(1)
                    or item_match.group(2)).strip()
            # Skip empty or very short items
            if len(text) < 3:
                continue
            # Skip items that look like code or metadata
            if text.startswith("```") or text.startswith("---"):
                continue
            items.append({
                "phase": current_phase,
                "task_text": text,
                "sort_order": sort_order,
            })
            sort_order += 1

    return items


def import_plan(
    file_path: str | None = None,
    from_claude: bool = False,
    project_name: str | None = None,
    clear: bool = False,
):
    """Import a plan file into the DB."""
    project_id, proj_name = _resolve_project(project_name)

    if clear:
        db.execute(
            "DELETE FROM plan_items WHERE project_id = ?",
            (project_id,),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Cleared existing plan for "
            + click.style(proj_name, bold=True)
        )

    if from_claude:
        # Scan ~/.claude/plans/ for files, try to match to project
        plans_dir = Path.home() / ".claude" / "plans"
        if not plans_dir.is_dir():
            raise click.ClickException(
                f"No plans directory found at {plans_dir}"
            )

        # Get project path for matching
        row = db.query(
            "SELECT path FROM projects WHERE id = ?",
            (project_id,),
        )
        proj_path = row[0]["path"] if row else ""

        found = []
        for f in sorted(plans_dir.glob("*.md")):
            content = f.read_text()
            # Check if the plan mentions this project's path or name
            if proj_name in content or proj_path in content:
                found.append(f)

        if not found:
            click.echo(
                click.style("•", fg="cyan")
                + f" No Claude plans found referencing "
                + click.style(proj_name, bold=True)
            )
            return

        click.echo(
            click.style("•", fg="cyan")
            + f" Found {len(found)} plan(s) for "
            + click.style(proj_name, bold=True)
            + ":"
        )
        for f in found:
            click.echo(f"  {f.name}")

        # Import the most recent one (last alphabetically,
        # which is a rough proxy)
        plan_file = found[-1]
        click.echo(
            click.style("•", fg="cyan")
            + f" Importing {plan_file.name}"
        )
        content = plan_file.read_text()
        _do_import(project_id, proj_name, content, str(plan_file))

    elif file_path:
        p = Path(file_path).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        content = p.read_text()
        _do_import(project_id, proj_name, content, str(p))

    else:
        # Try PLAN.md in project directory
        row = db.query(
            "SELECT path FROM projects WHERE id = ?",
            (project_id,),
        )
        if row:
            plan_path = Path(row[0]["path"]) / "PLAN.md"
            if plan_path.exists():
                content = plan_path.read_text()
                _do_import(
                    project_id, proj_name, content,
                    str(plan_path),
                )
                return

        raise click.ClickException(
            "No plan file specified. Use:\n"
            "  endless plan import <file>\n"
            "  endless plan import --from-claude\n"
            "  Or create a PLAN.md in the project directory."
        )


def _do_import(
    project_id: int, proj_name: str,
    content: str, source_file: str,
):
    items = _parse_plan_markdown(content)

    if not items:
        click.echo(
            click.style("•", fg="cyan")
            + " No plan items found in file."
        )
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    # Filter out completed items
    active_items = [
        i for i in items if i["phase"] != "completed"
    ]

    for item in active_items:
        title = item["task_text"][:80]
        db.execute(
            "INSERT INTO plan_items "
            "(project_id, phase, title, task_text, status, "
            "source_file, sort_order, created_at) "
            "VALUES (?, ?, ?, ?, 'pending', ?, ?, ?)",
            (project_id, item["phase"], title, item["task_text"],
             source_file, item["sort_order"], now),
        )

    # Count by phase
    phases = {}
    for item in active_items:
        phases[item["phase"]] = phases.get(item["phase"], 0) + 1

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {len(active_items)} plan item(s) "
        + f"for {click.style(proj_name, bold=True)}"
    )
    for phase, count in sorted(phases.items()):
        click.echo(f"  {phase}: {count}")


def show_plan(
    project_name: str | None = None,
    show_all: bool = False,
    plan_id: int = 0,
):
    """Show a plan for a project."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE pi.project_id = ? AND pi.plan_id = ?"
    params: list = [project_id, plan_id]
    if not show_all:
        where += " AND pi.status != 'completed'"

    rows = db.query(
        f"SELECT pi.id, pi.phase, COALESCE(pi.title, pi.task_text) as title, "
        f"pi.task_text, pi.status, "
        f"pi.created_at, pi.completed_at "
        f"FROM plan_items pi {where} "
        f"ORDER BY pi.phase, pi.sort_order",
        tuple(params),
    )

    # Get sub-plan info for items that have sub-plans
    sub_plan_counts = {}
    sub_rows = db.query(
        "SELECT parent_item_id, count(*) as total, "
        "sum(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as done "
        "FROM plan_items WHERE project_id = ? AND parent_item_id IS NOT NULL "
        "GROUP BY parent_item_id",
        (project_id,),
    )
    for sr in sub_rows:
        sub_plan_counts[sr["parent_item_id"]] = (
            sr["total"], sr["done"]
        )

    if not rows:
        if plan_id == 0:
            click.echo(
                click.style("•", fg="cyan")
                + f" No plan items for "
                + click.style(proj_name, bold=True)
            )
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No items in sub-plan {plan_id}"
            )
        return

    # Header
    click.echo()
    if plan_id == 0:
        click.echo(
            click.style(f"Plan for {proj_name}", bold=True)
        )
    else:
        # plan_id = parent_item_id
        parent = db.query(
            "SELECT task_text FROM plan_items WHERE id = ?",
            (plan_id,),
        )
        parent_text = parent[0]["task_text"] if parent else f"sub-plan {plan_id}"
        click.echo(
            click.style(f"Sub-plan: {parent_text}", bold=True)
        )

    current_phase = None
    for row in rows:
        if row["phase"] != current_phase:
            current_phase = row["phase"]
            click.echo()
            click.echo(click.style(
                f"  [{current_phase.upper()}]", bold=True, fg="cyan"
            ))

        status_indicators = {
            "pending": click.style("○", dim=True),
            "in_progress": click.style("◉", fg="yellow"),
            "completed": click.style("●", fg="green"),
            "blocked": click.style("✗", fg="red"),
        }
        indicator = status_indicators.get(
            row["status"], "?"
        )

        item_id = row["id"]
        task = row["title"]
        id_str = click.style(f"#{item_id}", dim=True)

        # Sub-plan indicator
        sub_info = ""
        if item_id in sub_plan_counts:
            total, done = sub_plan_counts[item_id]
            sub_info = click.style(
                f" ▸ sub-plan ({total} items, {done} done)",
                fg="cyan",
            )

        click.echo(f"  {indicator} {id_str} {task}{sub_info}")

    click.echo()
    total = len(rows)
    completed = sum(1 for r in rows if r["status"] == "completed")
    click.echo(click.style(
        f"{total} item(s)"
        + (f", {completed} completed" if completed else ""),
        dim=True,
    ))


def add_item(
    task_text: str,
    title: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    plan_id: int = 0,
):
    """Add a single plan item."""
    project_id, proj_name = _resolve_project(project_name)
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    # Default title to first 80 chars of task_text
    if not title:
        title = task_text[:80]

    # Determine sort_order
    if after:
        row = db.query(
            "SELECT sort_order FROM plan_items WHERE id = ?",
            (after,),
        )
        if row:
            sort_order = row[0]["sort_order"] + 5
        else:
            sort_order = _next_sort_order(project_id, phase, plan_id)
    else:
        sort_order = _next_sort_order(project_id, phase, plan_id)

    # Sub-plan: plan_id IS the parent_item_id
    parent_item_id = plan_id if plan_id > 0 else None

    cursor = db.execute(
        "INSERT INTO plan_items "
        "(project_id, phase, title, task_text, status, sort_order, "
        "plan_id, parent_item_id, created_at) "
        "VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?)",
        (project_id, phase, title, task_text, sort_order,
         plan_id, parent_item_id, now),
    )
    item_id = cursor.lastrowid
    click.echo(
        click.style("•", fg="cyan")
        + f" Added #{item_id}: {title}"
    )


def create_sub_plan(
    parent_item_id: int,
    project_name: str | None = None,
):
    """Create a sub-plan for a parent item. Returns the plan_id."""
    project_id, proj_name = _resolve_project(project_name)

    # Verify parent exists
    row = db.query(
        "SELECT id, task_text FROM plan_items WHERE id = ?",
        (parent_item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {parent_item_id}"
        )

    # Use parent_item_id as the plan_id (simple 1:1 mapping)
    new_plan_id = parent_item_id

    click.echo(
        click.style("•", fg="cyan")
        + f" Created sub-plan for "
        + click.style(f"#{parent_item_id}: {row[0]['task_text']}", bold=True)
    )
    click.echo(
        f"  Add items with: endless plan add \"task\" --plan {new_plan_id}"
    )
    return new_plan_id


def import_json(
    data: list[dict],
    project_name: str | None = None,
    clear: bool = False,
):
    """Import plan items from a JSON array."""
    project_id, proj_name = _resolve_project(project_name)

    if clear:
        db.execute(
            "DELETE FROM plan_items WHERE project_id = ?",
            (project_id,),
        )

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    count = 0
    for i, item in enumerate(data):
        text = item.get("text", item.get("task_text", ""))
        if not text:
            continue
        title = item.get("title", text[:80])
        phase = item.get("phase", "now")
        status = item.get("status", "pending")
        db.execute(
            "INSERT INTO plan_items "
            "(project_id, phase, title, task_text, status, sort_order, created_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?)",
            (project_id, phase, title, text, status, i * 10, now),
        )
        count += 1

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count} item(s) for "
        + click.style(proj_name, bold=True)
    )


def remove_item(item_id: int):
    """Remove a plan item."""
    row = db.query(
        "SELECT id, task_text FROM plan_items WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    db.execute("DELETE FROM plan_items WHERE id = ?", (item_id,))
    click.echo(
        click.style("•", fg="cyan")
        + f" Removed: {row[0]['task_text']}"
    )


def _next_sort_order(project_id: int, phase: str, plan_id: int = 0) -> int:
    val = db.scalar(
        "SELECT MAX(sort_order) FROM plan_items "
        "WHERE project_id = ? AND phase = ? AND plan_id = ?",
        (project_id, phase, plan_id),
    )
    return (val or 0) + 10


def complete_item(item_id: int):
    """Mark a plan item as completed."""
    row = db.query(
        "SELECT id, task_text, status FROM plan_items "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    if row[0]["status"] == "completed":
        click.echo(
            click.style("•", fg="cyan")
            + f" Item #{item_id} is already completed"
        )
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE plan_items SET status='completed', "
        "completed_at=? WHERE id=?",
        (now, item_id),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Completed: {row[0]['task_text']}"
    )


def start_item(item_id: int):
    """Mark a plan item as in progress."""
    row = db.query(
        "SELECT id, task_text FROM plan_items WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    db.execute(
        "UPDATE plan_items SET status='in_progress' "
        "WHERE id=?",
        (item_id,),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Started: {row[0]['task_text']}"
    )
