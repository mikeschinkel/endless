"""Plan command logic — import, show, and manage plan items."""

import os
import re
from datetime import datetime, timezone
from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config


def plan_id_display(item_id: int) -> str:
    """Format a plan ID for display: E-123"""
    return f"E-{item_id}"


def parse_plan_id(value: str) -> int:
    """Parse a plan ID from user input, stripping optional E- prefix."""
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
    """Import a plan file into the DB."""
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
            "  endless plan import <file>\n"
            "  endless plan import --from-claude\n"
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
            + " No plan items found in file."
        )
        return

    if replace:
        # Delete items from the same source file AND all their
        # descendants (which may be from other source files).
        # First null out parent references to avoid FK issues,
        # then delete.
        db.execute(
            "UPDATE plans SET parent_id = NULL "
            "WHERE parent_id IN ("
            "  WITH RECURSIVE tree(id) AS ("
            "    SELECT id FROM plans"
            "    WHERE project_id = ? AND source_file = ?"
            "    UNION ALL"
            "    SELECT pi.id FROM plans pi"
            "    JOIN tree t ON pi.parent_id = t.id"
            "  ) SELECT id FROM tree"
            ")",
            (project_id, source_file),
        )
        db.execute(
            "DELETE FROM plans WHERE id IN ("
            "  SELECT id FROM plans"
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
                "INSERT INTO plans "
                "(project_id, phase, title, description, status, "
                "source_file, sort_order, parent_id, created_at) "
                "VALUES (?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?)",
                (project_id, node["phase"], title, node["text"],
                 source_file, node["sort_order"], db_parent_id, now),
            )
            count[0] += 1
            new_id = cursor.lastrowid
            if node["children"]:
                _insert_tree(node["children"], new_id)

    _insert_tree(tree, parent_id)

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count[0]} plan item(s) "
        + f"for {click.style(proj_name, bold=True)}"
    )


def show_plan(
    project_name: str | None = None,
    show_all: bool = False,
):
    """Show a plan for a project as a tree."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE pi.project_id = ?"
    params: list = [project_id]
    if not show_all:
        where += " AND pi.status != 'completed'"

    rows = db.query(
        f"SELECT pi.id, pi.phase, COALESCE(pi.title, pi.description) as title, "
        f"pi.description, pi.status, pi.parent_id, "
        f"pi.created_at, pi.completed_at "
        f"FROM plans pi {where} "
        f"ORDER BY pi.sort_order",
        tuple(params),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan")
            + f" No plan items for "
            + click.style(proj_name, bold=True)
        )
        return

    # Build tree from flat rows
    by_id = {r["id"]: r for r in rows}
    children_of: dict[int | None, list] = {}
    for row in rows:
        pid = row["parent_id"]
        children_of.setdefault(pid, []).append(row)

    # Header
    click.echo()
    click.echo(
        click.style(f"Plan for {proj_name}", bold=True)
    )

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
            id_str = click.style(plan_id_display(row['id']), dim=True)
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


def add_item(
    title: str,
    description: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    parent_id: int | None = None,
):
    """Add a single plan."""
    project_id, proj_name = _resolve_project(project_name)
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")

    # Determine sort_order
    if after:
        row = db.query(
            "SELECT sort_order FROM plans WHERE id = ?",
            (after,),
        )
        if row:
            sort_order = row[0]["sort_order"] + 5
        else:
            sort_order = _next_sort_order(project_id, phase)
    else:
        sort_order = _next_sort_order(project_id, phase)

    cursor = db.execute(
        "INSERT INTO plans "
        "(project_id, phase, title, description, status, sort_order, "
        "parent_id, created_at) "
        "VALUES (?, ?, ?, ?, 'needs_plan', ?, ?, ?)",
        (project_id, phase, title, description, sort_order,
         parent_id, now),
    )
    item_id = cursor.lastrowid
    click.echo(
        click.style("•", fg="cyan")
        + f" Added {plan_id_display(item_id)}: {title}"
    )



def import_json(
    data: list[dict],
    project_name: str | None = None,
    clear: bool = False,
):
    """Import plan items from a JSON array."""
    project_id, proj_name = _resolve_project(project_name)

    if clear:
        db.execute(
            "DELETE FROM plans WHERE project_id = ?",
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
            "INSERT INTO plans "
            "(project_id, phase, title, description, status, sort_order, created_at) "
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
        "SELECT id, description FROM plans WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    db.execute("DELETE FROM plans WHERE id = ?", (item_id,))
    click.echo(
        click.style("•", fg="cyan")
        + f" Removed: {row[0]['description']}"
    )


def _next_sort_order(project_id: int, phase: str) -> int:
    val = db.scalar(
        "SELECT MAX(sort_order) FROM plans "
        "WHERE project_id = ? AND phase = ?",
        (project_id, phase),
    )
    return (val or 0) + 10


def complete_item(item_id: int):
    """Mark a plan item as completed."""
    row = db.query(
        "SELECT id, description, status FROM plans "
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
            + f" Item {plan_id_display(item_id)} is already completed"
        )
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")
    db.execute(
        "UPDATE plans SET status='completed', "
        "completed_at=? WHERE id=?",
        (now, item_id),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" Completed: {row[0]['description']}"
    )


def start_item(item_id: int):
    """Mark a plan item as in progress."""
    row = db.query(
        "SELECT id, description FROM plans WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    db.execute(
        "UPDATE plans SET status='in_progress' "
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
):
    """Update fields on a plan."""
    row = db.query(
        "SELECT id, title FROM plans WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan found with id {item_id}"
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
        f"UPDATE plans SET {', '.join(updates)} WHERE id = ?",
        tuple(params),
    )

    changed = [u.split(" =")[0] for u in updates]
    click.echo(
        click.style("•", fg="cyan")
        + f" Updated {plan_id_display(item_id)}: {', '.join(changed)}"
    )


def detail_item(item_id: int):
    """Show full detail for a plan item."""
    row = db.query(
        "SELECT id, title, description, phase, status, "
        "parent_id, source_file, prompt, created_at, "
        "completed_at FROM plans WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )

    item = row[0]
    click.echo()
    click.echo(click.style(
        f"{plan_id_display(item['id'])} — {item['title']}", bold=True,
    ))
    click.echo(click.style(
        f"Status: {item['status']}  Phase: {item['phase']}",
        dim=True,
    ))
    if item["parent_id"]:
        click.echo(click.style(
            f"Parent: {plan_id_display(item['parent_id'])}", dim=True,
        ))
    if item["source_file"]:
        click.echo(click.style(
            f"Source: {item['source_file']}", dim=True,
        ))
    click.echo()

    # Show description
    if item["description"] and item["description"] != item["title"]:
        click.echo(click.style("— Detail —", fg="cyan"))
        click.echo(item["description"])
        click.echo()

    # Show prompt if present
    if item["prompt"]:
        click.echo(click.style("— Prompt —", fg="cyan"))
        click.echo(item["prompt"])
        click.echo()


def show_prompt(item_id: int):
    """Output just the prompt text for a plan item."""
    row = db.query(
        "SELECT prompt FROM plans WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan item found with id {item_id}"
        )
    if not row[0]["prompt"]:
        raise click.ClickException(
            f"No prompt set for item {plan_id_display(item_id)}"
        )
    # Raw output, no decoration — suitable for piping
    click.echo(row[0]["prompt"])


def spawn_plan(item_id: int, project_name: str | None = None):
    """Spawn a new tmux window with Claude working on a plan's prompt."""
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
        "FROM plans p "
        "JOIN projects proj ON p.project_id = proj.id "
        "WHERE p.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No plan found with id {item_id}"
        )
    item = row[0]
    if not item["prompt"]:
        raise click.ClickException(
            f"No prompt set for plan {plan_id_display(item_id)}. "
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
         "@endless_plan_id", str(item_id)],
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

    # Launch claude
    subprocess.run(
        ["tmux", "send-keys", "-t", window_name,
         "claude", "Enter"],
        check=True,
    )

    # Wait for Claude to start
    import time
    time.sleep(2)

    # Enter plan mode
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
        ["tmux", "send-keys", "-t", window_name, ""],
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
        + click.style(f"{plan_id_display(item_id)}: {title}", bold=True)
    )
    click.echo(
        f"  Switch to it: tmux select-window -t {window_name}"
    )


def start_chat():
    """Start a chat-only session (no task tracking required)."""
    click.echo(
        click.style("•", fg="cyan")
        + " Chat session started. Write operations are allowed without task tracking."
    )
