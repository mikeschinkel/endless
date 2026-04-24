"""Task command logic — import, show, and manage task items."""

import os
import re
from datetime import datetime, timezone
from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config


_TITLE_VERBS = {
    "add", "apply", "audit", "build", "capture", "clean", "configure", "consolidate", "convert",
    "create", "decide", "define", "defer", "deploy", "design", "disable",
    "distinguish", "document", "enable", "enforce", "evaluate", "expand",
    "extract", "fix", "implement", "improve", "integrate", "investigate",
    "merge", "migrate", "move", "package", "print", "read", "redesign", "refactor", "remove",
    "rename", "render", "replace", "require", "research", "resolve",
    "show", "simplify", "split", "support", "surface", "sync", "test", "track",
    "update", "validate",
}


def validate_title(title: str, force: bool = False):
    """Warn if title doesn't start with an actionable verb."""
    first_word = title.split()[0].lower() if title.strip() else ""
    if first_word not in _TITLE_VERBS:
        if force:
            return
        raise click.ClickException(
            f"Title should start with an actionable verb, got '{first_word}'.\n"
            f"  Common verbs: add, fix, implement, design, refactor, remove, build, …\n"
            f"  Use --force to bypass."
        )


def task_id_display(item_id: int) -> str:
    """Format a task ID for display: E-123"""
    return f"E-{item_id}"


def parse_task_id(value: str) -> int:
    """Parse a task ID from user input, stripping optional E- prefix."""
    s = value.strip()
    if s.upper().startswith("E-"):
        s = s[2:]
    return int(s)


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
                "Specify a name: endless task <command> "
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


def _phase_for_heading(text: str) -> str:
    """Map a heading's text to a phase name."""
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
    lower = text.lower()
    lower = re.sub(
        r"^(phase \d+|step \d+)\s*[—–:-]\s*", "", lower,
    )
    for key, phase in phase_map.items():
        if key in lower:
            return phase
    return "now"


def _parse_plan_markdown(content: str) -> list[dict]:
    """Parse a markdown plan file into a tree of items.

    Headings become parent items (goals/branches).
    Bullets nest under the nearest heading.
    Nested bullets nest under their parent bullet.

    Returns list of {text, title, phase, sort_order, depth, children: [...]}
    """
    root_children: list[dict] = []
    # Stack tracks the current nesting context:
    # each entry is (depth, node) where node has a "children" list
    stack: list[tuple[int, dict]] = []
    current_phase = "now"
    heading_depth = 0  # depth of the most recent heading
    sort_order = 0
    in_code_block = False
    last_node: dict | None = None  # most recent node (heading or bullet)
    last_node_indent = 0  # indent level of the last node (0 for headings)
    prose_lines: list[str] = []  # accumulating prose for last node

    def _flush_prose():
        """Set accumulated prose as the text field (title already has the item text)."""
        nonlocal prose_lines
        if last_node and prose_lines:
            # Strip trailing blank lines
            while prose_lines and not prose_lines[-1]:
                prose_lines.pop()
            if prose_lines:
                last_node["text"] = "\n".join(prose_lines)
        prose_lines = []

    for line in content.splitlines():
        stripped = line.rstrip()

        # Track fenced code blocks
        if stripped.startswith("```"):
            in_code_block = not in_code_block
            continue
        if in_code_block:
            continue

        # Detect headings → become items themselves
        heading_match = re.match(r"^(#{1,6})\s+(.+)$", stripped)
        if heading_match:
            _flush_prose()
            depth = len(heading_match.group(1))
            text = heading_match.group(2).strip()
            current_phase = _phase_for_heading(text)
            if current_phase == "_skip":
                continue
            heading_depth = depth
            node = {
                "text": text,
                "title": text[:80],
                "phase": current_phase,
                "sort_order": sort_order,
                "depth": depth,
                "children": [],
            }
            sort_order += 1
            # Pop stack back to a depth < this heading
            while stack and stack[-1][0] >= depth:
                stack.pop()
            if stack:
                stack[-1][1]["children"].append(node)
            else:
                root_children.append(node)
            stack.append((depth, node))
            last_node = node
            last_node_indent = 0
            continue

        if current_phase == "_skip":
            continue

        # Detect list items (bullet or numbered)
        item_match = re.match(
            r"^(\s*)[-*]\s+(.+)$|^(\s*)\d+[.)]\s+(.+)$", stripped
        )
        if item_match:
            _flush_prose()
            if item_match.group(1) is not None:
                indent = len(item_match.group(1))
                text = item_match.group(2).strip()
            else:
                indent = len(item_match.group(3))
                text = item_match.group(4).strip()
            if len(text) < 3:
                continue
            if text.startswith("```") or text.startswith("---"):
                continue

            # Bullet depth is always relative to the current heading,
            # not the previous bullet. Each 2 spaces of indent adds 1 level.
            bullet_depth = heading_depth + 1 + (indent // 2)

            node = {
                "text": text,
                "title": text[:80],
                "phase": current_phase,
                "sort_order": sort_order,
                "depth": bullet_depth,
                "children": [],
            }
            sort_order += 1

            # Pop stack back to a depth < this bullet
            while stack and stack[-1][0] >= bullet_depth:
                stack.pop()
            if stack:
                stack[-1][1]["children"].append(node)
            else:
                root_children.append(node)
            stack.append((bullet_depth, node))
            last_node = node
            last_node_indent = indent
            continue

        # Prose lines: non-heading, non-bullet text after a heading or bullet.
        # Must be indented (for bullets: more than the bullet; for headings:
        # any indentation), or be a blank line continuing a prose block.
        if last_node is not None:
            if stripped == "":
                # Blank line — include in prose if we already have some
                if prose_lines:
                    prose_lines.append("")
                continue
            # Check if line is indented (prose continuation)
            line_indent = len(line) - len(line.lstrip())
            if line_indent > last_node_indent:
                prose_lines.append(stripped.strip())
                continue

        # Non-indented prose or unattached text — reset prose tracking
        _flush_prose()
        last_node = None

    _flush_prose()
    return root_children


def import_plan(
    file_path: str | None = None,
    from_claude: bool = False,
    project_name: str | None = None,
    replace: bool = False,
    parent_id: int | None = None,
):
    """Import a task file into the DB."""
    project_id, proj_name = _resolve_project(project_name)

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
        _do_import(
            project_id, proj_name, content, str(plan_file),
            replace=replace, parent_id=parent_id,
        )

    elif file_path:
        p = Path(file_path).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        content = p.read_text()
        _do_import(
            project_id, proj_name, content, str(p),
            replace=replace, parent_id=parent_id,
        )

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
                    replace=replace, parent_id=parent_id,
                )
                return

        raise click.ClickException(
            "No plan file specified. Use:\n"
            "  endless task import <file>\n"
            "  endless task import --from-claude\n"
            "  Or create a PLAN.md in the project directory."
        )


def _do_import(
    project_id: int, proj_name: str,
    content: str, source_file: str,
    replace: bool = False,
    parent_id: int | None = None,
):
    tree = _parse_plan_markdown(content)

    if not tree:
        click.echo(
            click.style("•", fg="cyan")
            + " No task items found in file."
        )
        return

    if replace:
        # Delete items from the same source file AND all their
        # descendants (which may be from other source files).
        # First null out parent references to avoid FK issues,
        # then delete.
        db.execute(
            "UPDATE tasks SET parent_id = NULL "
            "WHERE parent_id IN ("
            "  WITH RECURSIVE tree(id) AS ("
            "    SELECT id FROM tasks"
            "    WHERE project_id = ? AND source_file = ?"
            "    UNION ALL"
            "    SELECT pi.id FROM tasks pi"
            "    JOIN tree t ON pi.parent_id = t.id"
            "  ) SELECT id FROM tree"
            ")",
            (project_id, source_file),
        )
        db.execute(
            "DELETE FROM tasks WHERE id IN ("
            "  SELECT id FROM tasks"
            "  WHERE project_id = ? AND source_file = ?"
            ")",
            (project_id, source_file),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Replaced items from {Path(source_file).name}"
            + f" for {click.style(proj_name, bold=True)}"
        )

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    count = [0]  # mutable counter for recursion

    def _insert_tree(nodes: list[dict], db_parent_id: int | None):
        for node in nodes:
            if node["phase"] == "completed":
                continue
            title = node["title"]
            cursor = db.execute(
                "INSERT INTO tasks "
                "(project_id, phase, title, description, status, "
                "source_file, sort_order, parent_id, created_at, updated_at) "
                "VALUES (?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?, ?)",
                (project_id, node["phase"], title, node["text"],
                 source_file, node["sort_order"], db_parent_id, now, now),
            )
            count[0] += 1
            new_id = cursor.lastrowid
            if node["children"]:
                _insert_tree(node["children"], new_id)

    _insert_tree(tree, parent_id)

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count[0]} task item(s) "
        + f"for {click.style(proj_name, bold=True)}"
    )


def _render_flat_table(rows):
    """Render rows as a flat table with ID, Phase, Status, Title columns."""
    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 80
    id_w = max(2, max(len(task_id_display(r["id"])) for r in rows))
    ph_w = max(5, max(len(r["phase"]) for r in rows))
    st_w = max(6, max(len(r["status"]) for r in rows))
    gap = "  "
    fixed_width = id_w + ph_w + st_w + len(gap) * 3
    title_width = max(20, term_width - fixed_width)
    display_titles = []
    for row in rows:
        title = row["title"]
        if len(title) > title_width:
            title = title[:title_width - 1] + "…"
        display_titles.append(title)
    max_title_len = max(len(t) for t in display_titles) if display_titles else 5
    click.echo(
        f"{'ID':<{id_w}}{gap}{'Phase':<{ph_w}}{gap}{'Status':<{st_w}}{gap}Title"
    )
    click.echo(
        f"{'─'*id_w}{gap}{'─'*ph_w}{gap}{'─'*st_w}{gap}{'─'*max_title_len}"
    )
    for row, title in zip(rows, display_titles):
        click.echo(
            f"{task_id_display(row['id']):<{id_w}}{gap}"
            f"{row['phase']:<{ph_w}}{gap}"
            f"{row['status']:<{st_w}}{gap}{title}"
        )


def show_plan(
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: str | None = None,
    phase_filter: str | None = None,
    sort_by: str | None = None,
    llm: bool = False,
    as_json: bool = False,
):
    """Show tasks for a project as a tree, or flat sorted list."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE pi.project_id = ?"
    params: list = [project_id]
    if not show_all:
        where += " AND pi.status != 'completed'"
    if status_filter:
        where += " AND pi.status = ?"
        params.append(status_filter)
    if phase_filter:
        where += " AND pi.phase = ?"
        params.append(phase_filter)

    sort_col_map = {
        "id": "pi.id",
        "status": "pi.status",
        "phase": "CASE pi.phase WHEN 'now' THEN 0 WHEN 'next' THEN 1 WHEN 'later' THEN 2 ELSE 3 END",
        "created": "pi.created_at",
        "title": "pi.title",
    }
    order_by = sort_col_map.get(sort_by, "pi.sort_order")

    rows = db.query(
        f"SELECT pi.id, pi.phase, COALESCE(pi.title, pi.description) as title, "
        f"pi.description, pi.status, pi.parent_id, "
        f"pi.created_at, pi.completed_at "
        f"FROM tasks pi {where} "
        f"ORDER BY {order_by}",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo(f"# {proj_name}\n(no tasks)")
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No tasks for "
                + click.style(proj_name, bold=True)
            )
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "phase": row["phase"],
                "status": row["status"],
                "title": row["title"],
                "parent": f"E-{row['parent_id']}" if row["parent_id"] else None,
                "created": row["created_at"],
                "completed": row["completed_at"] or None,
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {proj_name}")
        for row in rows:
            click.echo(
                f"E-{row['id']} {row['phase']} "
                f"{row['status']} {row['title']}"
            )
        return

    # Header
    click.echo()
    click.echo(
        click.style(f"Tasks for {proj_name}", bold=True)
    )

    if sort_by:
        _render_flat_table(rows)
    else:
        # Tree output
        by_id = {r["id"]: r for r in rows}
        children_of: dict[int | None, list] = {}
        for row in rows:
            pid = row["parent_id"]
            if pid is not None and pid not in by_id:
                pid = None
            children_of.setdefault(pid, []).append(row)

        status_indicators = {
            "needs_plan": click.style("○", fg="yellow"),
            "ready": click.style("●", fg="green"),
            "revisit": click.style("?", fg="cyan"),
            "in_progress": click.style("◉", fg="blue"),
            "verify": click.style("◉", fg="magenta"),
            "completed": click.style("●", fg="green"),
            "blocked": click.style("✗", fg="red"),
        }

        def _render(parent_id: int | None, indent: int):
            for row in children_of.get(parent_id, []):
                indicator = status_indicators.get(row["status"], "?")
                id_str = click.style(task_id_display(row['id']), dim=True)
                phase_str = click.style(f"[{row['phase']}]", fg="cyan")
                pad = "  " * indent
                click.echo(
                    f"{pad}{indicator} {id_str} {phase_str} {row['title']}"
                )
                _render(row["id"], indent + 1)

        _render(None, 1)

    click.echo()
    total = len(rows)
    completed = sum(1 for r in rows if r["status"] == "completed")
    click.echo(click.style(
        f"{total} item(s)"
        + (f", {completed} completed" if completed else ""),
        dim=True,
    ))


def next_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int = 10,
    llm: bool = False,
    as_json: bool = False,
):
    """Show top actionable leaf tasks, ranked by priority."""
    where = (
        "WHERE t.status NOT IN ('completed', 'blocked') "
        "AND (SELECT count(*) FROM tasks c WHERE c.parent_id = t.id) = 0 "
        "AND t.id NOT IN ("
        "  SELECT td.source_id FROM task_deps td"
        "  WHERE td.source_type = 'task' AND td.dep_type = 'needs'"
        "    AND td.target_id IN ("
        "      SELECT t2.id FROM tasks t2 WHERE t2.status != 'completed'"
        "    )"
        ")"
    )
    params: list = []

    if not show_all:
        # Default: scope to current project (or explicit --project)
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)
    elif project_name:
        # --all with --project makes no sense, but --project wins
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)

    params.append(limit)

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status, p.name as project_name "
        f"FROM tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY "
        f"  CASE t.status "
        f"    WHEN 'in_progress' THEN 0 WHEN 'verify' THEN 1 "
        f"    WHEN 'ready' THEN 2 WHEN 'needs_plan' THEN 3 "
        f"    WHEN 'revisit' THEN 4 ELSE 5 END, "
        f"  CASE t.phase "
        f"    WHEN 'now' THEN 0 WHEN 'next' THEN 1 "
        f"    WHEN 'later' THEN 2 ELSE 3 END, "
        f"  t.updated_at DESC "
        f"LIMIT ?",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo("# no actionable tasks")
        else:
            click.echo(
                click.style("•", fg="cyan") + " No actionable tasks"
            )
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "phase": row["phase"],
                "status": row["status"],
                "title": row["title"],
                "project": row["project_name"],
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    status_indicators = {
        "needs_plan": "○",
        "ready": "●",
        "revisit": "?",
        "in_progress": "◉",
        "verify": "◉",
    }

    # Group by project
    groups: dict[str, list] = {}
    for row in rows:
        groups.setdefault(row["project_name"], []).append(row)

    for proj, items in groups.items():
        if llm:
            click.echo(f"# {proj}")
            for item in items:
                click.echo(
                    f"E-{item['id']} {item['phase']} "
                    f"{item['status']} {item['title']}"
                )
        else:
            click.echo()
            click.echo(click.style(f"Next up ({proj}):", bold=True))
            _render_flat_table(items)
    if not llm:
        click.echo()


def recent_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int = 10,
    llm: bool = False,
    as_json: bool = False,
):
    """Show most recently updated tasks."""
    where = "WHERE 1=1"
    params: list = []

    if not show_all:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)
    elif project_name:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)

    params.append(limit)

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status, p.name as project_name "
        f"FROM tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY t.updated_at DESC "
        f"LIMIT ?",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo("# no recent tasks")
        else:
            click.echo(
                click.style("•", fg="cyan") + " No recent tasks"
            )
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "phase": row["phase"],
                "status": row["status"],
                "title": row["title"],
                "project": row["project_name"],
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    # Group by project
    groups: dict[str, list] = {}
    for row in rows:
        groups.setdefault(row["project_name"], []).append(row)

    for proj, items in groups.items():
        if llm:
            click.echo(f"# {proj}")
            for item in items:
                click.echo(
                    f"E-{item['id']} {item['phase']} "
                    f"{item['status']} {item['title']}"
                )
        else:
            click.echo()
            click.echo(click.style(f"Recent ({proj}):", bold=True))
            _render_flat_table(items)
    if not llm:
        click.echo()


def add_item(
    title: str,
    description: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    parent_id: int | None = None,
    task_type: str | None = None,
    force: bool = False,
):
    """Add a single task."""
    validate_title(title, force=force)
    project_id, proj_name = _resolve_project(project_name)
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    task_type = task_type or "task"

    # Determine sort_order
    if after:
        row = db.query(
            "SELECT sort_order FROM tasks WHERE id = ?",
            (after,),
        )
        if row:
            sort_order = row[0]["sort_order"] + 5
        else:
            sort_order = _next_sort_order(project_id, phase)
    else:
        sort_order = _next_sort_order(project_id, phase)

    cursor = db.execute(
        "INSERT INTO tasks "
        "(project_id, phase, title, description, status, type, sort_order, "
        "parent_id, created_at, updated_at) "
        "VALUES (?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?, ?)",
        (project_id, phase, title, description, task_type, sort_order,
         parent_id, now, now),
    )
    item_id = cursor.lastrowid
    click.echo(
        click.style("•", fg="cyan")
        + f" Added {task_id_display(item_id)}: {title}"
    )



def import_json(
    data: list[dict],
    project_name: str | None = None,
    clear: bool = False,
):
    """Import task items from a JSON array."""
    project_id, proj_name = _resolve_project(project_name)

    if clear:
        db.execute(
            "DELETE FROM tasks WHERE project_id = ?",
            (project_id,),
        )

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    count = 0
    for i, item in enumerate(data):
        text = item.get("text", item.get("description", ""))
        if not text:
            continue
        title = item.get("title", text[:80])
        phase = item.get("phase", "now")
        status = item.get("status", "needs_plan")
        db.execute(
            "INSERT INTO tasks "
            "(project_id, phase, title, description, status, sort_order, created_at, updated_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            (project_id, phase, title, text, status, i * 10, now, now),
        )
        count += 1

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count} item(s) for "
        + click.style(proj_name, bold=True)
    )


def remove_item(item_id: int):
    """Remove a task."""
    row = db.query(
        "SELECT id, description FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    db.execute("DELETE FROM tasks WHERE id = ?", (item_id,))
    click.echo(
        click.style("•", fg="cyan")
        + f" Removed: {row[0]['description']}"
    )


def _next_sort_order(project_id: int, phase: str) -> int:
    val = db.scalar(
        "SELECT MAX(sort_order) FROM tasks "
        "WHERE project_id = ? AND phase = ?",
        (project_id, phase),
    )
    return (val or 0) + 10


def complete_item(item_id: int, cascade: bool = False):
    """Mark a task as completed."""
    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    if row[0]["status"] == "completed" and not cascade:
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already completed"
        )
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    if cascade:
        # Complete all descendants recursively
        count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree "
            "JOIN tasks ON tasks.id = tree.id "
            "WHERE tasks.status != 'completed'",
            (item_id,),
        ) or 0
        db.execute(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") UPDATE tasks SET status='completed', completed_at=? "
            "WHERE id IN (SELECT id FROM tree) AND status != 'completed'",
            (item_id, now),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Completed {task_id_display(item_id)} and {count - 1} descendant(s): {row[0]['title']}"
        )
    else:
        db.execute(
            "UPDATE tasks SET status='completed', "
            "completed_at=? WHERE id=?",
            (now, item_id),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Completed: {row[0]['title']}"
        )


def start_item(item_id: int):
    """Mark a task as in progress."""
    row = db.query(
        "SELECT id, description FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    db.execute(
        "UPDATE tasks SET status='in_progress' "
        "WHERE id=?",
        (item_id,),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Started: {row[0]['description']}"
    )


def update_plan(
    item_id: int,
    status: str | None = None,
    title: str | None = None,
    description: str | None = None,
    text_file: str | None = None,
    prompt_file: str | None = None,
    parent_id: int | None = None,
    force: bool = False,
):
    """Update fields on a task."""
    if title is not None:
        validate_title(title, force=force)
    row = db.query(
        "SELECT id, title FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    updates = []
    params = []

    if status is not None:
        valid = ("needs_plan", "ready", "in_progress",
                 "verify", "completed", "blocked", "revisit")
        if status not in valid:
            raise click.ClickException(
                f"Invalid status '{status}'. "
                f"Valid: {', '.join(valid)}"
            )
        updates.append("status = ?")
        params.append(status)
        if status == "completed":
            now = datetime.now(timezone.utc).strftime(
                "%Y-%m-%dT%H:%M:%S"
            )
            updates.append("completed_at = ?")
            params.append(now)

    if title is not None:
        updates.append("title = ?")
        params.append(title)

    if description is not None:
        updates.append("description = ?")
        params.append(description)

    if text_file is not None:
        p = Path(text_file).expanduser()
        if not p.exists():
            raise click.ClickException(
                f"File not found: {p}"
            )
        updates.append("text = ?")
        params.append(p.read_text())

    if prompt_file is not None:
        p = Path(prompt_file).expanduser()
        if not p.exists():
            raise click.ClickException(
                f"File not found: {p}"
            )
        updates.append("prompt = ?")
        params.append(p.read_text())

    if parent_id is not None:
        updates.append("parent_id = ?")
        params.append(parent_id if parent_id > 0 else None)

    if not updates:
        raise click.ClickException(
            "Nothing to update. Specify at least one flag."
        )

    params.append(item_id)
    db.execute(
        f"UPDATE tasks SET {', '.join(updates)} WHERE id = ?",
        tuple(params),
    )

    changed = [u.split(" =")[0] for u in updates]
    click.echo(
        click.style("•", fg="cyan")
        + f" Updated {task_id_display(item_id)}: {', '.join(changed)}"
    )


def _format_timestamp(ts: str) -> str:
    """Format an ISO timestamp as '2026-04-19 2:35 pm'."""
    if not ts:
        return ""
    try:
        dt = datetime.strptime(ts, "%Y-%m-%dT%H:%M:%S")
        return dt.strftime("%Y-%m-%d %-I:%M %p").lower()
    except ValueError:
        return ts


def detail_item(
    item_id: int,
    show_description: bool = True,
    show_text: bool = False,
    show_prompt: bool = False,
    show_children: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Show full detail for a task."""
    row = db.query(
        "SELECT id, title, description, text, phase, status, type, "
        "parent_id, source_file, prompt, created_at, updated_at, "
        "completed_at, sort_order FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    item = row[0]

    if as_json:
        import json
        out = {
            "id": f"E-{item['id']}",
            "title": item["title"],
            "type": item["type"],
            "phase": item["phase"],
            "status": item["status"],
            "parent": f"E-{item['parent_id']}" if item["parent_id"] else None,
            "created": item["created_at"],
            "updated": item["updated_at"],
            "completed": item["completed_at"] or None,
            "source_file": item["source_file"] or None,
            "description": item["description"] if show_description else None,
            "text": item["text"] if show_text else None,
            "prompt": item["prompt"] if show_prompt else None,
        }
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? AND status != 'completed' "
                "ORDER BY sort_order",
                (item_id,),
            )
            out["children"] = [
                {"id": f"E-{c['id']}", "title": c["title"],
                 "status": c["status"], "phase": c["phase"]}
                for c in children
            ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# E-{item['id']} {item['title']}")
        click.echo(f"type={item['type']} phase={item['phase']} "
                    f"status={item['status']}")
        if item["parent_id"]:
            click.echo(f"parent=E-{item['parent_id']}")
        click.echo(f"created={item['created_at']}")
        click.echo(f"updated={item['updated_at']}")
        if item["completed_at"]:
            click.echo(f"completed={item['completed_at']}")
        if show_description and item["description"] and item["description"] != item["title"]:
            click.echo(f"\n## Description\n{item['description']}")
        if show_text and item["text"]:
            click.echo(f"\n## Text\n{item['text']}")
        if show_prompt and item["prompt"]:
            click.echo(f"\n## Prompt\n{item['prompt']}")
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? AND status != 'completed' "
                "ORDER BY id",
                (item_id,),
            )
            if children:
                click.echo("\n## Children")
                for c in children:
                    click.echo(f"E-{c['id']} {c['phase']} {c['status']} {c['title']}")
        return

    # Human-readable output
    label = lambda s: click.style(s, fg="cyan")
    val = lambda s: click.style(str(s), fg="white", bold=True)

    click.echo()
    click.echo(click.style("Task Detail", fg="green", bold=True))
    click.echo(click.style("───────────", dim=True))

    click.echo(f"{label('ID:'):<19} {val(task_id_display(item['id']))}")
    click.echo(f"{label('Title:'):<19} {val(item['title'])}")
    click.echo(f"{label('Type:'):<19} {val(item['type'])}")
    click.echo(f"{label('Phase:'):<19} {val(item['phase'])}")
    click.echo(f"{label('Status:'):<19} {val(item['status'])}")
    if item["parent_id"]:
        click.echo(f"{label('Parent:'):<19} {val(task_id_display(item['parent_id']))}")
    click.echo(f"{label('Created:'):<19} {val(_format_timestamp(item['created_at']))}")
    if item["updated_at"] and item["updated_at"] != item["created_at"]:
        click.echo(f"{label('Updated:'):<19} {val(_format_timestamp(item['updated_at']))}")
    if item["completed_at"]:
        click.echo(f"{label('Completed:'):<19} {val(_format_timestamp(item['completed_at']))}")
    if item["source_file"]:
        click.echo(f"{label('Source:'):<19} {val(item['source_file'])}")

    # Large text sections
    if show_description and item["description"] and item["description"] != item["title"]:
        click.echo()
        click.echo(click.style("— Description —", fg="cyan"))
        click.echo(item["description"])

    if show_text and item["text"]:
        click.echo()
        click.echo(click.style("— Text —", fg="cyan"))
        click.echo(item["text"])

    if show_prompt and item["prompt"]:
        click.echo()
        click.echo(click.style("— Prompt —", fg="cyan"))
        click.echo(item["prompt"])

    if show_children:
        children = db.query(
            "SELECT id, COALESCE(title, description) as title, status, phase "
            "FROM tasks WHERE parent_id = ? AND status != 'completed' "
            "ORDER BY id",
            (item_id,),
        )
        click.echo()
        click.echo(click.style("— Children —", fg="cyan"))
        if children:
            _render_flat_table(children)
        else:
            click.echo("(none)")

    click.echo()


def show_prompt(item_id: int):
    """Output just the prompt text for a task."""
    row = db.query(
        "SELECT prompt FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )
    if not row[0]["prompt"]:
        raise click.ClickException(
            f"No prompt set for item {task_id_display(item_id)}"
        )
    # Raw output, no decoration — suitable for piping
    click.echo(row[0]["prompt"])


def spawn_plan(item_id: int, project_name: str | None = None, no_plan: bool = False):
    """Spawn a new tmux window with Claude working on a task's prompt."""
    import shutil
    import subprocess
    import tempfile

    # Verify tmux is available and we're in a tmux session
    if not shutil.which("tmux"):
        raise click.ClickException("tmux is not installed")
    if not os.environ.get("TMUX"):
        raise click.ClickException(
            "Not in a tmux session. "
            "endless spawn requires tmux."
        )

    # Get the plan item and its prompt
    row = db.query(
        "SELECT p.id, p.title, p.prompt, p.project_id, "
        "proj.path as project_path "
        "FROM tasks p "
        "JOIN projects proj ON p.project_id = proj.id "
        "WHERE p.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )
    item = row[0]
    if not item["prompt"]:
        raise click.ClickException(
            f"No prompt set for task {task_id_display(item_id)}. "
            f"Set one first."
        )

    project_path = item["project_path"]
    title = item["title"]

    # Create a short window name from the title
    window_name = re.sub(r"[^a-zA-Z0-9]", "-", title.lower())[:30]

    # Write prompt to a temp file for tmux load-buffer
    prompt_file = tempfile.NamedTemporaryFile(
        mode="w", suffix=".md", prefix="endless-prompt-",
        delete=False,
    )
    prompt_file.write(item["prompt"])
    prompt_file.close()

    # Create tmux window and set plan metadata
    subprocess.run(
        ["tmux", "new-window", "-n", window_name],
        check=True,
    )
    subprocess.run(
        ["tmux", "set", "-w", "-t", window_name,
         "@endless_task_id", str(item_id)],
        check=True,
    )
    subprocess.run(
        ["tmux", "set", "-w", "-t", window_name,
         "@endless_project_id", str(item["project_id"])],
        check=True,
    )

    # cd to project directory
    subprocess.run(
        ["tmux", "send-keys", "-t", window_name,
         f"cd {project_path}", "Enter"],
        check=True,
    )

    # Launch claude (use binary directly to avoid shell function wrappers)
    claude_bin = os.path.expanduser("~/.local/bin/claude")
    if not os.path.exists(claude_bin):
        claude_bin = "claude"
    subprocess.run(
        ["tmux", "send-keys", "-t", window_name,
         claude_bin, "Enter"],
        check=True,
    )

    # Wait for Claude to start
    import time
    time.sleep(5)

    # Enter plan mode unless --no-plan
    if not no_plan:
        subprocess.run(
            ["tmux", "send-keys", "-t", window_name,
             "/plan", "Enter"],
            check=True,
        )
        time.sleep(1)

    # Load the prompt into tmux buffer and paste it
    subprocess.run(
        ["tmux", "load-buffer", prompt_file.name],
        check=True,
    )
    subprocess.run(
        ["tmux", "paste-buffer", "-t", window_name],
        check=True,
    )

    # Send Enter to submit the prompt
    subprocess.run(
        ["tmux", "send-keys", "-t", window_name,
         "Enter"],
        check=True,
    )

    # Clean up temp file
    os.unlink(prompt_file.name)

    click.echo(
        click.style("•", fg="cyan")
        + f" Spawned window '{window_name}' for "
        + click.style(f"{task_id_display(item_id)}: {title}", bold=True)
    )
    click.echo(
        f"  Switch to it: tmux select-window -t {window_name}"
    )


def move_task(
    item_id: int | None = None,
    parent: int | None = None,
    root: bool = False,
    with_children: bool = False,
    children_of: int | None = None,
    project_name: str | None = None,
):
    """Move tasks between parents, to root, or batch-move children."""
    # Validation: must specify exactly one destination
    if not parent and not root:
        raise click.ClickException(
            "Must specify either --parent or --root as the destination."
        )
    if parent and root:
        raise click.ClickException(
            "Cannot specify both --parent and --root."
        )

    # Validation: children-of vs item_id
    if children_of and item_id:
        raise click.ClickException(
            "Cannot specify both item_id and --children-of."
        )
    if not children_of and not item_id:
        raise click.ClickException(
            "Must specify either an item_id or --children-of."
        )
    if with_children and not item_id:
        raise click.ClickException(
            "--with-children requires an item_id."
        )

    # Resolve target parent
    target_parent_id = None
    if parent:
        row = db.query(
            "SELECT id FROM tasks WHERE id = ?",
            (parent,),
        )
        if not row:
            raise click.ClickException(
                f"Target parent {task_id_display(parent)} not found."
            )
        target_parent_id = parent

    bullet = click.style("•", fg="cyan")

    if children_of:
        # Verify source parent exists
        row = db.query(
            "SELECT id FROM tasks WHERE id = ?",
            (children_of,),
        )
        if not row:
            raise click.ClickException(
                f"Source parent {task_id_display(children_of)} not found."
            )

        # Count children
        count = db.scalar(
            "SELECT count(*) FROM tasks WHERE parent_id = ?",
            (children_of,),
        ) or 0
        if count == 0:
            click.echo(
                bullet
                + f" {task_id_display(children_of)} has no children to move."
            )
            return

        # Move children
        db.execute(
            "UPDATE tasks SET parent_id = ? WHERE parent_id = ?",
            (target_parent_id, children_of),
        )
        dest = task_id_display(target_parent_id) if target_parent_id else "root"
        click.echo(
            bullet
            + f" Moved {count} children of {task_id_display(children_of)} to {dest}"
        )
        return

    # Single task move (with or without children)
    # Verify task exists
    row = db.query(
        "SELECT id, parent_id FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"Task {task_id_display(item_id)} not found."
        )

    # Circular move check: can't move a task under itself or its descendant
    if target_parent_id:
        # Walk up from target_parent_id to make sure item_id is not an ancestor
        current = target_parent_id
        while current is not None:
            if current == item_id:
                raise click.ClickException(
                    f"Cannot move {task_id_display(item_id)} under "
                    f"{task_id_display(target_parent_id)}: would create a cycle."
                )
            ancestor = db.query(
                "SELECT parent_id FROM tasks WHERE id = ?",
                (current,),
            )
            current = ancestor[0]["parent_id"] if ancestor else None

    # Move the task
    db.execute(
        "UPDATE tasks SET parent_id = ? WHERE id = ?",
        (target_parent_id, item_id),
    )

    dest = task_id_display(target_parent_id) if target_parent_id else "root"
    suffix = " (with children)" if with_children else ""
    click.echo(
        bullet
        + f" Moved {task_id_display(item_id)} under {dest}{suffix}"
    )


def start_chat():
    """Start a chat-only session (no task tracking required)."""
    click.echo(
        click.style("•", fg="cyan")
        + " Chat session started. Write operations are allowed without task tracking."
    )
