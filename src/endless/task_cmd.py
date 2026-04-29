"""Task command logic — import, show, and manage task items."""

import os
import re
import uuid
from datetime import datetime, timezone
from pathlib import Path

import click
from tabulate import tabulate

from endless import db, config


_TIER_LABELS = {0: "n/a", 1: "auto", 2: "quick", 3: "deep", 4: "discuss"}
_TIER_FROM_LABEL = {v: k for k, v in _TIER_LABELS.items()}

# Sentinel meaning "tier IS NULL" for filtering
TIER_NONE = -1
# Sentinel meaning "clear tier to NULL" for update
TIER_CLEAR = -2
# Sentinel meaning "parent_id IS NULL" (root tasks only)
PARENT_NONE = 0


# Task relation vocabulary (E-957/E-958; informs dropped per E-1003).
# display_name -> (stored_dep_type, swap_source_target)
# Stored types are active voice (source is the actor): blocks, implements,
# replaces, relates_to. Inverse views (blocked_by, implemented_by, etc.) resolve to
# the same stored row queried with source/target swapped.
CANONICAL_DEP_TYPES: dict[str, tuple[str, bool]] = {
    "blocks":          ("blocks",     False),  # source blocks target
    "blocked_by":      ("blocks",     True),   # inverse view
    "implements":      ("implements", False),  # source implements target
    "implemented_by":  ("implements", True),
    "replaces":        ("replaces",   False),  # source replaces target
    "replaced_by":     ("replaces",   True),
    "relates_to":      ("relates_to", False),  # symmetric
}

# The 4 canonical stored types (the values in CANONICAL_DEP_TYPES, deduplicated).
STORED_DEP_TYPES = ("blocks", "implements", "replaces", "relates_to")

# Display order for `task show` — actionability descending; symmetric last.
RELATION_DISPLAY_ORDER = (
    "blocked_by", "blocks",
    "implements", "implemented_by",
    "replaces",   "replaced_by",
    "relates_to",
)

# Human-readable label for each display name (used in `task show` headings).
RELATION_LABELS = {
    "blocked_by":     "Blocked by",
    "blocks":         "Blocks",
    "implements":     "Implements",
    "implemented_by": "Implemented by",
    "replaces":       "Replaces",
    "replaced_by":    "Replaced by",
    "relates_to":     "Relates to",
}


def parse_tier(value: str) -> int:
    """Parse a tier value from user input: accepts none (NULL), 0/n/a, 1-4, or label names."""
    s = value.strip().lower()
    if s == "none":
        return TIER_CLEAR  # reset to NULL (untriaged)
    if s in _TIER_FROM_LABEL:
        return _TIER_FROM_LABEL[s]
    try:
        n = int(s)
        if n in _TIER_LABELS:
            return n
    except ValueError:
        pass
    valid = ", ".join(f"{k}={v}" for k, v in _TIER_LABELS.items())
    raise click.ClickException(
        f"Invalid tier '{value}'. Valid: none, {valid}"
    )


def parse_tier_filter(value: str) -> int:
    """Parse a tier value for filtering: accepts none/0, 1-4, or label names."""
    s = value.strip().lower()
    if s in ("none", "0"):
        return TIER_NONE
    if s in _TIER_FROM_LABEL:
        return _TIER_FROM_LABEL[s]
    try:
        n = int(s)
        if n in _TIER_LABELS:
            return n
    except ValueError:
        pass
    valid = ", ".join(f"{k}={v}" for k, v in _TIER_LABELS.items())
    raise click.ClickException(
        f"Invalid tier '{value}'. Valid: none, {valid}"
    )


def parse_parent_filter(value: str) -> int:
    """Parse a --parent value: 'none' for root tasks, or a task ID (E-NNN or NNN)."""
    s = value.strip().lower()
    if s == "none":
        return PARENT_NONE
    if s.startswith("e-"):
        s = s[2:]
    try:
        return int(s)
    except ValueError:
        raise click.ClickException(
            f"Invalid parent '{value}'. Expected 'none' or a task ID (e.g. E-799)"
        )


def tier_display(tier: int | None) -> str:
    """Format a tier for display: '1 (auto)'."""
    if tier is None:
        return ""
    label = _TIER_LABELS.get(tier, "?")
    return f"{tier} ({label})"


_TITLE_VERBS = {
    "accept", "add", "apply", "assume", "audit", "backfill", "build", "capture", "change", "clean", "clear", "confirm", "configure", "consolidate", "convert",
    "create", "decide", "define", "defer", "deploy", "design", "disable",
    "distinguish", "document", "enable", "enforce", "evaluate", "expand",
    "extract", "fix", "generate", "implement", "improve", "integrate", "investigate",
    "hide", "increase", "merge", "migrate", "move", "omit", "package", "print", "prune", "raise", "read", "reconcile", "redesign", "refactor", "remove",
    "rename", "render", "replace", "require", "research", "resolve",
    "search", "show", "simplify", "skip", "split", "support", "surface", "sync", "test", "track",
    "update", "validate", "verify",
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


def _project_root_for_task(task_id: int) -> Path | None:
    """Return the registered project root path for the project owning this task."""
    row = db.query(
        "SELECT p.path FROM projects p "
        "JOIN tasks t ON t.project_id = p.id "
        "WHERE t.id = ? LIMIT 1",
        (task_id,),
    )
    if not row:
        return None
    return Path(row[0]["path"]).expanduser()


def _write_task_plan_file(task_id: int, content: str) -> None:
    """Write a stable per-task copy of plan content to <project>/.endless/plans/<task-id>.md.

    The DB's tasks.text column remains source of truth; this file is an
    endless-owned, predictable export at a path that doesn't get clobbered
    by harness plan-file naming.
    """
    root = _project_root_for_task(task_id)
    if root is None:
        return  # task lookup failed; emit_event would have raised below anyway
    plans_dir = root / ".endless" / "plans"
    plans_dir.mkdir(parents=True, exist_ok=True)
    target = plans_dir / f"E-{task_id}.md"
    target.write_text(content)


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
        "done": "confirmed",
        "completed": "confirmed",
        "confirmed": "confirmed",
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
    from endless.event_bridge import emit_event

    tree = _parse_plan_markdown(content)

    if not tree:
        click.echo(
            click.style("•", fg="cyan")
            + " No task items found in file."
        )
        return

    if replace:
        emit_event(
            kind="task.bulk_cleared",
            project=proj_name,
            entity_type="task",
            entity_id="0",
            payload={"source_file": source_file},
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Replaced items from {Path(source_file).name}"
            + f" for {click.style(proj_name, bold=True)}"
        )

    count = [0]

    def _insert_tree(nodes: list[dict], db_parent_id: int | None):
        for node in nodes:
            if node["phase"] == "confirmed":
                continue
            title = node["title"]
            result = emit_event(
                kind="task.imported",
                project=proj_name,
                entity_type="task",
                entity_id="0",
                payload={
                    "title": title,
                    "description": node["text"],
                    "phase": node["phase"],
                    "status": "needs_plan",
                    "source_file": source_file,
                    "sort_order": node["sort_order"],
                    "parent_id": db_parent_id,
                },
            )
            count[0] += 1
            new_id = int(result["id"].replace("E-", ""))
            if node["children"]:
                _insert_tree(node["children"], new_id)

    _insert_tree(tree, parent_id)

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count[0]} task item(s) "
        + f"for {click.style(proj_name, bold=True)}"
    )


def _render_flat_table(rows):
    """Render rows as a flat table with ID, Phase, Status, Tier, Title columns."""
    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 80

    # Check if any rows have tier data (column may not exist in all queries)
    has_tier = (
        rows and "tier" in rows[0].keys()
        and any(r["tier"] is not None for r in rows)
    )

    id_w = max(2, max(len(task_id_display(r["id"])) for r in rows))
    ph_w = max(5, max(len(r["phase"]) for r in rows))
    st_w = max(6, max(len(r["status"]) for r in rows))
    ti_w = max(4, max(
        (len(_TIER_LABELS.get(r["tier"], "-")) if r["tier"] is not None else 1)
        for r in rows
    )) if has_tier else 0
    gap = "  "
    fixed_width = id_w + ph_w + st_w + len(gap) * 3
    if has_tier:
        fixed_width += ti_w + len(gap)
    title_width = max(20, term_width - fixed_width)
    display_titles = []
    for row in rows:
        title = row["title"]
        if len(title) > title_width:
            title = title[:title_width - 1] + "…"
        display_titles.append(title)
    max_title_len = max(len(t) for t in display_titles) if display_titles else 5

    header = f"{'ID':<{id_w}}{gap}{'Phase':<{ph_w}}{gap}{'Status':<{st_w}}"
    sep = f"{'─'*id_w}{gap}{'─'*ph_w}{gap}{'─'*st_w}"
    if has_tier:
        header += f"{gap}{'Tier':<{ti_w}}"
        sep += f"{gap}{'─'*ti_w}"
    header += f"{gap}Title"
    sep += f"{gap}{'─'*max_title_len}"
    click.echo(header)
    click.echo(sep)

    for row, title in zip(rows, display_titles):
        line = (
            f"{task_id_display(row['id']):<{id_w}}{gap}"
            f"{row['phase']:<{ph_w}}{gap}"
            f"{row['status']:<{st_w}}"
        )
        if has_tier:
            tier_val = row["tier"]
            tier_str = _TIER_LABELS.get(tier_val, "-") if tier_val is not None else "-"
            line += f"{gap}{tier_str:<{ti_w}}"
        line += f"{gap}{title}"
        click.echo(line)


def show_plan(
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: list[str] | None = None,
    phase_filter: str | None = None,
    tier_filter: int | None = None,
    parent_id: int | None = None,
    related_to_id: int | None = None,
    rel_type: str | None = None,
    sort_by: str | None = None,
    tree: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Show tasks for a project as a tree, or flat sorted list."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE pi.project_id = ? AND pi.type != 'decision'"
    params: list = [project_id]
    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND pi.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += " AND pi.status NOT IN ('confirmed', 'assumed', 'declined', 'obsolete')"
    if phase_filter:
        where += " AND pi.phase = ?"
        params.append(phase_filter)
    if tier_filter is not None:
        if tier_filter == TIER_NONE:
            where += " AND pi.tier IS NULL"
        else:
            where += " AND pi.tier = ?"
            params.append(tier_filter)
    if parent_id is not None:
        if parent_id == PARENT_NONE:
            where += " AND pi.parent_id IS NULL"
        else:
            where += " AND pi.parent_id = ?"
            params.append(parent_id)
    if related_to_id is not None:
        related_ids = _related_task_ids(related_to_id, rel_type)
        if not related_ids:
            # No related tasks — empty result via impossible WHERE
            where += " AND 0 = 1"
        else:
            placeholders = ",".join("?" for _ in related_ids)
            where += f" AND pi.id IN ({placeholders})"
            params.extend(related_ids)

    sort_col_map = {
        "id": "pi.id",
        "status": "pi.status",
        "phase": "CASE pi.phase WHEN 'now' THEN 0 WHEN 'next' THEN 1 WHEN 'later' THEN 2 ELSE 3 END",
        "tier": "CASE WHEN pi.tier IS NULL THEN 99 ELSE pi.tier END",
        "created": "pi.created_at",
        "title": "pi.title",
    }
    if not tree and not sort_by:
        sort_by = "id"
    order_by = sort_col_map.get(sort_by, "pi.sort_order")

    rows = db.query(
        f"SELECT pi.id, pi.phase, COALESCE(pi.title, pi.description) as title, "
        f"pi.description, pi.status, pi.parent_id, "
        f"pi.created_at, pi.completed_at, pi.tier "
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
                "tier": row["tier"],
                "title": row["title"],
                "parent": f"E-{row['parent_id']}" if row["parent_id"] else None,
                "created": row["created_at"],
                "confirmed": row["completed_at"] or None,
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {proj_name}")
        for row in rows:
            tier_val = row["tier"]
            tier_str = f" tier={_TIER_LABELS[tier_val]}" if tier_val else ""
            click.echo(
                f"E-{row['id']} {row['phase']} "
                f"{row['status']}{tier_str} {row['title']}"
            )
        return

    # Header
    click.echo()
    click.echo(
        click.style(f"Tasks for {proj_name}", bold=True)
    )

    if not tree:
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
            "confirmed": click.style("●", fg="green"),
            "blocked": click.style("✗", fg="red"),
        }

        def _render(parent_id: int | None, indent: int):
            for row in children_of.get(parent_id, []):
                indicator = status_indicators.get(row["status"], "?")
                id_str = click.style(task_id_display(row['id']), dim=True)
                phase_str = click.style(f"[{row['phase']}]", fg="cyan")
                tier_val = row["tier"] if "tier" in row.keys() else None
                tier_str = f" {click.style(f'[{_TIER_LABELS[tier_val]}]', fg='magenta')}" if tier_val else ""
                pad = "  " * indent
                click.echo(
                    f"{pad}{indicator} {id_str} {phase_str}{tier_str} {row['title']}"
                )
                _render(row["id"], indent + 1)

        _render(None, 1)

    click.echo()
    total = len(rows)
    confirmed = sum(1 for r in rows if r["status"] == "confirmed")
    click.echo(click.style(
        f"{total} item(s)"
        + (f", {confirmed} confirmed" if confirmed else ""),
        dim=True,
    ))


def next_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int = 10,
    llm: bool = False,
    as_json: bool = False,
    tier: int | None = None,
    phase_filter: str | None = None,
    parent_id: int | None = None,
):
    """Show top actionable leaf tasks, ranked by priority."""
    where = (
        "WHERE t.type != 'decision' "
        "AND t.status NOT IN ('confirmed', 'assumed', 'blocked', 'declined', 'obsolete', 'in_progress', 'verify') "
        "AND (SELECT count(*) FROM tasks c WHERE c.parent_id = t.id) = 0 "
        "AND t.id NOT IN ("
        "  SELECT td.target_id FROM task_deps td"
        "  WHERE td.target_type = 'task' AND td.dep_type = 'blocks'"
        "    AND td.source_id IN ("
        "      SELECT t2.id FROM tasks t2 WHERE t2.status != 'confirmed'"
        "    )"
        ")"
    )
    params: list = []

    if tier is not None:
        if tier == TIER_NONE:
            where += " AND t.tier IS NULL"
        else:
            where += " AND t.tier = ?"
            params.append(tier)

    if phase_filter:
        where += " AND t.phase = ?"
        params.append(phase_filter)

    if parent_id is not None:
        if parent_id == PARENT_NONE:
            where += " AND t.parent_id IS NULL"
        else:
            where += " AND t.parent_id = ?"
            params.append(parent_id)

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
        f"t.status, t.tier, p.name as project_name "
        f"FROM tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY "
        f"  CASE t.phase "
        f"    WHEN 'now' THEN 0 WHEN 'next' THEN 1 "
        f"    WHEN 'later' THEN 2 ELSE 3 END, "
        f"  CASE t.status "
        f"    WHEN 'ready' THEN 0 WHEN 'needs_plan' THEN 1 "
        f"    WHEN 'revisit' THEN 2 ELSE 3 END, "
        f"  CASE WHEN t.tier IS NULL THEN 99 ELSE t.tier END, "
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


def active_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    llm: bool = False,
    as_json: bool = False,
    parent_id: int | None = None,
):
    """Show tasks that are in progress or awaiting verification."""
    where = "WHERE t.type != 'decision' AND t.status IN ('in_progress', 'verify')"
    params: list = []

    if parent_id is not None:
        if parent_id == PARENT_NONE:
            where += " AND t.parent_id IS NULL"
        else:
            where += " AND t.parent_id = ?"
            params.append(parent_id)

    if not show_all:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)
    elif project_name:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status, t.tier, p.name as project_name "
        f"FROM tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY "
        f"  CASE t.status "
        f"    WHEN 'in_progress' THEN 0 WHEN 'verify' THEN 1 END, "
        f"  t.updated_at DESC",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo("# no active tasks")
        else:
            click.echo(
                click.style("•", fg="cyan") + " No active tasks"
            )
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "phase": row["phase"],
                "status": row["status"],
                "tier": row["tier"],
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
            click.echo(click.style(f"Active ({proj}):", bold=True))
            _render_flat_table(items)
    if not llm:
        click.echo()


def recent_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int = 10,
    llm: bool = False,
    as_json: bool = False,
    parent_id: int | None = None,
):
    """Show most recently updated tasks."""
    where = "WHERE t.type != 'decision'"
    params: list = []

    if parent_id is not None:
        if parent_id == PARENT_NONE:
            where += " AND t.parent_id IS NULL"
        else:
            where += " AND t.parent_id = ?"
            params.append(parent_id)

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
        f"t.status, t.tier, p.name as project_name "
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


def list_decisions(
    project_name: str | None = None,
    show_all: bool = False,
    sort_by: str | None = None,
    llm: bool = False,
    as_json: bool = False,
):
    """List decisions for a project (or all projects with --all)."""
    where = "WHERE t.type = 'decision'"
    params: list = []

    if not show_all:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)
    elif project_name:
        project_id, proj_name = _resolve_project(project_name)
        where += " AND t.project_id = ?"
        params.append(project_id)
    else:
        proj_name = "all projects"

    sort_col_map = {
        "id": "t.id DESC",
        "created": "t.created_at DESC, t.id DESC",
        "title": "t.title",
    }
    order_by = sort_col_map.get(sort_by or "id", "t.id DESC")

    rows = db.query(
        f"SELECT t.id, COALESCE(t.title, t.description) as title, t.description, "
        f"t.created_at, p.name as project_name "
        f"FROM tasks t JOIN projects p ON t.project_id = p.id "
        f"{where} ORDER BY {order_by}",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo(f"# {proj_name}\n(no decisions)")
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No decisions for "
                + click.style(proj_name, bold=True)
            )
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "title": row["title"],
                "created": row["created_at"],
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {proj_name} decisions")
        for row in rows:
            prefix = f"[{row['project_name']}] " if show_all else ""
            click.echo(f"E-{row['id']} {prefix}{row['title']}")
    else:
        try:
            term_width = os.get_terminal_size().columns
        except OSError:
            term_width = 80

        id_w = max(2, max(len(task_id_display(r["id"])) for r in rows))
        date_w = max(7, max(len(_format_timestamp(r["created_at"])) for r in rows))
        gap = "  "
        fixed_width = id_w + date_w + len(gap) * 2
        if show_all:
            proj_w = max(7, max(len(r["project_name"]) for r in rows))
            fixed_width += proj_w + len(gap)
        title_width = max(20, term_width - fixed_width)

        display_titles = []
        for row in rows:
            title = row["title"]
            if len(title) > title_width:
                title = title[:title_width - 1] + "…"
            display_titles.append(title)

        header = f"{'ID':<{id_w}}{gap}{'Created':<{date_w}}"
        sep = f"{'─'*id_w}{gap}{'─'*date_w}"
        if show_all:
            header += f"{gap}{'Project':<{proj_w}}"
            sep += f"{gap}{'─'*proj_w}"
        max_title_len = max(len(t) for t in display_titles) if display_titles else 5
        header += f"{gap}Title"
        sep += f"{gap}{'─'*max_title_len}"
        click.echo(header)
        click.echo(sep)

        for row, title in zip(rows, display_titles):
            line = (
                f"{task_id_display(row['id']):<{id_w}}{gap}"
                f"{_format_timestamp(row['created_at']):<{date_w}}"
            )
            if show_all:
                line += f"{gap}{row['project_name']:<{proj_w}}"
            line += f"{gap}{title}"
            click.echo(line)


def add_item(
    title: str,
    description: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    parent_id: int | None = None,
    task_type: str | None = None,
    status: str | None = None,
    tier: int | None = None,
    force: bool = False,
):
    """Add a single task."""
    from endless.event_bridge import emit_event

    task_type = task_type or "task"
    if task_type != "decision":
        validate_title(title, force=force)
    _, proj_name = _resolve_project(project_name)
    status = status or ("ready" if tier == 1 else "needs_plan")

    payload = {
        "title": title,
        "description": description or "",
        "phase": phase,
        "status": status,
        "type": task_type,
    }
    if tier is not None:
        payload["tier"] = tier
    if parent_id is not None:
        payload["parent_id"] = parent_id
    if after is not None:
        payload["after_id"] = after

    result = emit_event(
        kind="task.created",
        project=proj_name,
        entity_type="task",
        entity_id="0",
        payload=payload,
    )
    item_id = int(result["id"].replace("E-", ""))
    click.echo(
        click.style("•", fg="cyan")
        + f" Added {task_id_display(item_id)}: {title}"
    )
    return item_id



def import_json(
    data: list[dict],
    project_name: str | None = None,
    clear: bool = False,
):
    """Import task items from a JSON array."""
    from endless.event_bridge import emit_event

    _, proj_name = _resolve_project(project_name)

    if clear:
        emit_event(
            kind="task.bulk_cleared",
            project=proj_name,
            entity_type="task",
            entity_id="0",
            payload={"source_file": "json_import"},
        )

    count = 0
    for i, item in enumerate(data):
        text = item.get("text", item.get("description", ""))
        if not text:
            continue
        title = item.get("title", text[:80])
        phase = item.get("phase", "now")
        status = item.get("status", "needs_plan")
        emit_event(
            kind="task.imported",
            project=proj_name,
            entity_type="task",
            entity_id="0",
            payload={
                "title": title,
                "description": text,
                "phase": phase,
                "status": status,
                "sort_order": i * 10,
                "source_file": "json_import",
            },
        )
        count += 1

    click.echo(
        click.style("•", fg="cyan")
        + f" Imported {count} item(s) for "
        + click.style(proj_name, bold=True)
    )


def remove_item(item_id: int, cascade: bool = False):
    """Remove a task."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    child_count = db.scalar(
        "SELECT count(*) FROM tasks WHERE parent_id = ?",
        (item_id,),
    ) or 0

    if child_count > 0 and not cascade:
        raise click.ClickException(
            f"Task {task_id_display(item_id)} has {child_count} child(ren). "
            f"Use --cascade to delete it and all descendants."
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.deleted",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "cascade": cascade,
            "title": row[0]["title"],
        },
    )

    if cascade and child_count > 0:
        desc_count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE parent_id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 0
        click.echo(
            click.style("•", fg="cyan")
            + f" Removed {task_id_display(item_id)} and {desc_count} descendant(s): {row[0]['title']}"
        )
    else:
        click.echo(
            click.style("•", fg="cyan")
            + f" Removed: {row[0]['title']}"
        )


def _next_sort_order(project_id: int, phase: str) -> int:
    val = db.scalar(
        "SELECT MAX(sort_order) FROM tasks "
        "WHERE project_id = ? AND phase = ?",
        (project_id, phase),
    )
    return (val or 0) + 10


def complete_item(item_id: int, cascade: bool = False):
    """Mark a task as confirmed."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    if row[0]["status"] == "confirmed" and not cascade:
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already confirmed"
        )
        return

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": row[0]["status"],
            "new_status": "confirmed",
            "cascade": cascade,
        },
    )

    if cascade:
        count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 1
        click.echo(
            click.style("•", fg="cyan")
            + f" Confirmed {task_id_display(item_id)} and {count - 1} descendant(s): {row[0]['title']}"
        )
    else:
        click.echo(
            click.style("•", fg="cyan")
            + f" Confirmed: {row[0]['title']}"
        )


def assume_item(item_id: int, cascade: bool = False):
    """Mark a task as assumed (believed complete, not yet verified)."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    if row[0]["status"] == "assumed" and not cascade:
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already assumed"
        )
        return

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": row[0]["status"],
            "new_status": "assumed",
            "cascade": cascade,
        },
    )

    if cascade:
        count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 1
        click.echo(
            click.style("•", fg="cyan")
            + f" Assumed {task_id_display(item_id)} and {count - 1} descendant(s): {row[0]['title']}"
        )
    else:
        click.echo(
            click.style("•", fg="cyan")
            + f" Assumed: {row[0]['title']}"
        )


def start_item(item_id: int):
    """Mark a task as in progress."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, description, status FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": row[0]["status"],
            "new_status": "in_progress",
        },
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
    phase: str | None = None,
    tier: int | None = None,
    force: bool = False,
):
    """Update fields on a task."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, title, status, type FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )
    if title is not None and row[0]["type"] != "decision":
        validate_title(title, force=force)

    # Validate status if provided
    if status is not None:
        valid = ("needs_plan", "ready", "in_progress",
                 "verify", "confirmed", "assumed", "blocked", "revisit", "declined", "obsolete")
        if status not in valid:
            raise click.ClickException(
                f"Invalid status '{status}'. "
                f"Valid: {', '.join(valid)}"
            )

    # Build the fields map for the event payload
    fields = {}
    changed_names = []

    if status is not None:
        fields["status"] = status
        changed_names.append("status")

    if phase is not None:
        fields["phase"] = phase
        changed_names.append("phase")

    if title is not None:
        fields["title"] = title
        changed_names.append("title")

    if description is not None:
        fields["description"] = description
        changed_names.append("description")

    if text_file is not None:
        p = Path(text_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        text_content = p.read_text()
        fields["text"] = text_content
        changed_names.append("text")
        _write_task_plan_file(item_id, text_content)

    if prompt_file is not None:
        p = Path(prompt_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        fields["prompt"] = p.read_text()
        changed_names.append("prompt")

    if parent_id is not None:
        fields["parent_id"] = parent_id if parent_id > 0 else None
        changed_names.append("parent_id")

    if tier is not None:
        if tier == TIER_CLEAR:
            fields["tier"] = None
        else:
            fields["tier"] = tier
            # Tier 1 tasks can't be needs_plan; auto-advance to ready
            if tier == 1 and status is None and row[0]["status"] == "needs_plan":
                fields["status"] = "ready"
                changed_names.append("status")
        changed_names.append("tier")

    if not fields:
        raise click.ClickException(
            "Nothing to update. Specify at least one flag."
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.fields_updated",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={"fields": fields},
    )

    click.echo(
        click.style("•", fg="cyan")
        + f" Updated {task_id_display(item_id)}: {', '.join(changed_names)}"
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
        "completed_at, sort_order, tier FROM tasks WHERE id = ?",
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
            "confirmed": item["completed_at"] or None,
            "source_file": item["source_file"] or None,
            "tier": item["tier"],
            "description": item["description"] if show_description else None,
            "text": item["text"] if show_text else None,
            "prompt": item["prompt"] if show_prompt else None,
        }
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? AND status != 'confirmed' "
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
        tier_str = f" tier={tier_display(item['tier'])}" if item["tier"] else ""
        click.echo(f"type={item['type']} phase={item['phase']} "
                    f"status={item['status']}{tier_str}")
        if item["parent_id"]:
            click.echo(f"parent=E-{item['parent_id']}")
        relations = get_all_relations(item_id)
        for display_name, items in relations.items():
            ids = ",".join(f"E-{d['id']}" for d in items)
            click.echo(f"{display_name}={ids}")
        click.echo(f"created={item['created_at']}")
        click.echo(f"updated={item['updated_at']}")
        if item["completed_at"]:
            click.echo(f"confirmed={item['completed_at']}")
        if show_description and item["description"] and item["description"] != item["title"]:
            click.echo(f"\n## Description\n{item['description']}")
        if show_text and item["text"]:
            click.echo(f"\n## Text\n{item['text']}")
        if show_prompt and item["prompt"]:
            click.echo(f"\n## Prompt\n{item['prompt']}")
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? AND status != 'confirmed' "
                "ORDER BY id",
                (item_id,),
            )
            if children:
                click.echo("\n## Children")
                for c in children:
                    click.echo(f"E-{c['id']} {c['phase']} {c['status']} {c['title']}")
        return

    # Human-readable output
    col_w = 13  # width of label column (longest: "Replaced by:" = 12 + 1 space)
    label = lambda s: click.style(f"{s:<{col_w}}", fg="cyan")
    val = lambda s: click.style(str(s), fg="white", bold=True)

    click.echo()
    click.echo(click.style("Task Detail", fg="green", bold=True))
    click.echo(click.style("───────────", dim=True))

    click.echo(f"{label('ID:')} {val(task_id_display(item['id']))}")
    click.echo(f"{label('Title:')} {val(item['title'])}")
    click.echo(f"{label('Type:')} {val(item['type'])}")
    click.echo(f"{label('Phase:')} {val(item['phase'])}")
    click.echo(f"{label('Status:')} {val(item['status'])}")
    if item["tier"]:
        click.echo(f"{label('Tier:')} {val(tier_display(item['tier']))}")
    if item["parent_id"]:
        click.echo(f"{label('Parent:')} {val(task_id_display(item['parent_id']))}")
    relations = get_all_relations(item_id)
    for display_name, items in relations.items():
        heading = RELATION_LABELS.get(display_name, display_name) + ":"
        dep_str = ", ".join(task_id_display(d["id"]) for d in items)
        click.echo(f"{label(heading)} {val(dep_str)}")
    click.echo(f"{label('Created:')} {val(_format_timestamp(item['created_at']))}")
    if item["updated_at"] and item["updated_at"] != item["created_at"]:
        click.echo(f"{label('Updated:')} {val(_format_timestamp(item['updated_at']))}")
    if item["completed_at"]:
        click.echo(f"{label('Confirmed:')} {val(_format_timestamp(item['completed_at']))}")
    if item["source_file"]:
        click.echo(f"{label('Source:')} {val(item['source_file'])}")

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
            "FROM tasks WHERE parent_id = ? AND status != 'confirmed' "
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


def search_tasks(
    query: str,
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: list[str] | None = None,
    phase_filter: str | None = None,
    parent_id: int | None = None,
    search_text: bool = False,
    search_prompt: bool = False,
    limit: int = 20,
    llm: bool = False,
    as_json: bool = False,
):
    """Search tasks by query string across ID, title, and description."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE t.project_id = ? AND t.type != 'decision'"
    params: list = [project_id]

    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND t.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += " AND t.status NOT IN ('confirmed', 'assumed', 'declined', 'obsolete')"
    if phase_filter:
        where += " AND t.phase = ?"
        params.append(phase_filter)
    if parent_id is not None:
        if parent_id == PARENT_NONE:
            where += " AND t.parent_id IS NULL"
        else:
            where += " AND t.parent_id = ?"
            params.append(parent_id)

    # Build search conditions
    like_pattern = f"%{query}%"
    search_clauses = [
        "COALESCE(t.title, '') LIKE ? COLLATE NOCASE",
        "COALESCE(t.description, '') LIKE ? COLLATE NOCASE",
    ]
    search_params = [like_pattern, like_pattern]

    # Check if query looks like a task ID (E-NNN or just NNN)
    id_query = query.strip()
    if id_query.upper().startswith("E-"):
        id_query = id_query[2:]
    try:
        task_id_num = int(id_query)
        search_clauses.append("t.id = ?")
        search_params.append(task_id_num)
    except ValueError:
        pass

    if search_text:
        search_clauses.append("COALESCE(t.text, '') LIKE ? COLLATE NOCASE")
        search_params.append(like_pattern)

    if search_prompt:
        search_clauses.append("COALESCE(t.prompt, '') LIKE ? COLLATE NOCASE")
        search_params.append(like_pattern)

    where += " AND (" + " OR ".join(search_clauses) + ")"
    params.extend(search_params)

    params.append(limit)
    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status "
        f"FROM tasks t "
        f"{where} "
        f"ORDER BY t.updated_at DESC "
        f"LIMIT ?",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo(f"# {proj_name}\n(no matches for '{query}')")
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No tasks matching '{query}' in "
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
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# {proj_name} search: {query}")
        for row in rows:
            click.echo(
                f"E-{row['id']} {row['phase']} "
                f"{row['status']} {row['title']}"
            )
        return

    click.echo()
    click.echo(
        click.style(f"Search results for '{query}' ({proj_name}):", bold=True)
    )
    _render_flat_table(rows)
    click.echo()
    click.echo(click.style(f"{len(rows)} match(es)", dim=True))


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
    from endless.event_bridge import emit_event

    # Verify task exists
    row = db.query(
        "SELECT id, parent_id FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"Task {task_id_display(item_id)} not found."
        )

    _, proj_name = _resolve_project(project_name)
    old_parent_id = row[0]["parent_id"]

    # Go executor handles circular reference check
    emit_event(
        kind="task.moved",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_parent_id": old_parent_id,
            "new_parent_id": target_parent_id,
        },
    )

    dest = task_id_display(target_parent_id) if target_parent_id else "root"
    suffix = " (with children)" if with_children else ""
    click.echo(
        bullet
        + f" Moved {task_id_display(item_id)} under {dest}{suffix}"
    )


def start_chat():
    """Start a chat-only session (no task tracking required)."""
    session_id = str(uuid.uuid4())
    cursor = db.execute(
        "INSERT INTO sessions (session_id, platform, state) "
        "VALUES (?, 'claude', 'working')",
        (session_id,),
    )
    row_id = cursor.lastrowid
    click.echo(
        click.style("•", fg="cyan")
        + f" Chat session started (session: {row_id})."
        + " Write operations are allowed without task tracking."
    )


# ── Task relations (E-957) ─────────────────────────────────────────


def link_tasks(source_id: int, target_id: int, dep_type: str):
    """Create a typed relationship between two tasks.

    `dep_type` is a display name from CANONICAL_DEP_TYPES. The function resolves
    it to (stored_type, swap) and inserts; if swap, source/target are swapped
    before insert so storage stays active-voice.
    """
    if dep_type not in CANONICAL_DEP_TYPES:
        valid = ", ".join(CANONICAL_DEP_TYPES)
        raise click.ClickException(
            f"Invalid relation type '{dep_type}'. Valid: {valid}"
        )
    if source_id == target_id:
        raise click.ClickException("A task cannot link to itself.")
    for tid in (source_id, target_id):
        if not db.exists("SELECT 1 FROM tasks WHERE id = ?", (tid,)):
            raise click.ClickException(f"Task {task_id_display(tid)} not found.")

    stored, swap = CANONICAL_DEP_TYPES[dep_type]
    src, tgt = (target_id, source_id) if swap else (source_id, target_id)

    try:
        db.execute(
            "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) "
            "VALUES ('task', ?, 'task', ?, ?)",
            (src, tgt, stored),
        )
    except Exception as e:
        if "UNIQUE" in str(e):
            raise click.ClickException(
                f"{task_id_display(source_id)} is already linked to {task_id_display(target_id)} as '{dep_type}'."
            )
        raise

    click.echo(
        click.style("•", fg="cyan")
        + f" Linked: {task_id_display(source_id)} {dep_type} {task_id_display(target_id)}"
    )


def unlink_tasks(source_id: int, target_id: int, dep_type: str | None = None):
    """Remove a typed relationship between two tasks.

    If `dep_type` is given, removes only that specific relation. If omitted:
      0 relations  → error
      1 relation   → remove it
      2+ relations → error listing them; require --as.
    """
    if dep_type is not None:
        if dep_type not in CANONICAL_DEP_TYPES:
            valid = ", ".join(CANONICAL_DEP_TYPES)
            raise click.ClickException(
                f"Invalid relation type '{dep_type}'. Valid: {valid}"
            )
        stored, swap = CANONICAL_DEP_TYPES[dep_type]
        src, tgt = (target_id, source_id) if swap else (source_id, target_id)
        result = db.execute(
            "DELETE FROM task_deps WHERE source_type = 'task' AND source_id = ? "
            "AND target_type = 'task' AND target_id = ? AND dep_type = ?",
            (src, tgt, stored),
        )
        if result.rowcount == 0:
            raise click.ClickException(
                f"No '{dep_type}' relation: {task_id_display(source_id)} → {task_id_display(target_id)}"
            )
        click.echo(
            click.style("•", fg="cyan")
            + f" Unlinked: {task_id_display(source_id)} no longer {dep_type} {task_id_display(target_id)}"
        )
        return

    rows = db.query(
        "SELECT source_id, target_id, dep_type FROM task_deps "
        "WHERE source_type = 'task' AND target_type = 'task' "
        "AND ((source_id = ? AND target_id = ?) OR (source_id = ? AND target_id = ?))",
        (source_id, target_id, target_id, source_id),
    )
    if not rows:
        raise click.ClickException(
            f"No relation between {task_id_display(source_id)} and {task_id_display(target_id)}."
        )
    if len(rows) > 1:
        names = []
        for r in rows:
            names.append(_relation_display_name_from(r, source_id))
        raise click.ClickException(
            f"Multiple relations between {task_id_display(source_id)} and "
            f"{task_id_display(target_id)} ({', '.join(names)}). Specify --as <type>."
        )

    row = rows[0]
    db.execute(
        "DELETE FROM task_deps WHERE source_type = 'task' AND source_id = ? "
        "AND target_type = 'task' AND target_id = ? AND dep_type = ?",
        (row["source_id"], row["target_id"], row["dep_type"]),
    )
    name = _relation_display_name_from(row, source_id)
    click.echo(
        click.style("•", fg="cyan")
        + f" Unlinked: {task_id_display(source_id)} ↔ {task_id_display(target_id)} ({name})"
    )


def _relation_display_name_from(row, perspective_id: int) -> str:
    """Pick the display name for a stored row from `perspective_id`'s point of view."""
    stored = row["dep_type"]
    # Symmetric: same name regardless of perspective
    for name, (st, swap) in CANONICAL_DEP_TYPES.items():
        if st == stored and not swap and st == "relates_to":
            return name
    # Asymmetric: pick swap=True when perspective is the target, swap=False when source
    want_swap = (row["target_id"] == perspective_id)
    for name, (st, swap) in CANONICAL_DEP_TYPES.items():
        if st == stored and swap == want_swap:
            return name
    return stored


def replace_task(old_id: int, new_id: int):
    """Mark old_id as replaced by new_id. Sets old to obsolete and records relationship."""
    if old_id == new_id:
        raise click.ClickException("A task cannot replace itself.")
    for tid in (old_id, new_id):
        if not db.exists("SELECT 1 FROM tasks WHERE id = ?", (tid,)):
            raise click.ClickException(f"Task {task_id_display(tid)} not found.")

    # "old replaced_by new" → display='replaced_by' resolves to stored='replaces' with
    # swap=True → row stored as source=new, target=old, dep_type='replaces' (active voice).
    try:
        link_tasks(old_id, new_id, "replaced_by")
    except click.ClickException as e:
        if "already linked" in str(e):
            raise click.ClickException(
                f"{task_id_display(old_id)} is already replaced by {task_id_display(new_id)}."
            )
        raise

    db.execute(
        "UPDATE tasks SET status = 'obsolete', tier = 0 WHERE id = ?",
        (old_id,),
    )

    click.echo(
        click.style("•", fg="cyan")
        + f" {task_id_display(old_id)} replaced by {task_id_display(new_id)} (set to obsolete)"
    )


def get_all_relations(item_id: int) -> dict[str, list]:
    """Return all relations for a task, keyed by display-name in fixed order.

    Each value is a list of dicts {id, title, status} for the related task.
    Empty groups are omitted from the result.
    """
    rows = db.query(
        "SELECT td.source_id, td.target_id, td.dep_type, "
        "       t_src.title as src_title, t_src.status as src_status, "
        "       t_tgt.title as tgt_title, t_tgt.status as tgt_status "
        "FROM   task_deps td "
        "JOIN   tasks t_src ON t_src.id = td.source_id "
        "JOIN   tasks t_tgt ON t_tgt.id = td.target_id "
        "WHERE  td.source_type = 'task' AND td.target_type = 'task' "
        "AND    (td.source_id = ? OR td.target_id = ?) "
        "ORDER BY td.dep_type, td.source_id, td.target_id",
        (item_id, item_id),
    )

    groups: dict[str, list] = {}
    for row in rows:
        is_source = (row["source_id"] == item_id)
        # Pick display name: swap=True when item is the target, swap=False when item is source
        want_swap = not is_source
        display = None
        for name, (stored, swap) in CANONICAL_DEP_TYPES.items():
            if stored == row["dep_type"] and swap == want_swap:
                display = name
                break
        if display is None:
            # Unknown stored dep_type — surface raw value
            display = row["dep_type"]

        # The "other" task in the relation
        other_id = row["target_id"] if is_source else row["source_id"]
        other_title = row["tgt_title"] if is_source else row["src_title"]
        other_status = row["tgt_status"] if is_source else row["src_status"]
        groups.setdefault(display, []).append(
            {"id": other_id, "title": other_title, "status": other_status}
        )

    # Return in fixed display order
    ordered: dict[str, list] = {}
    for name in RELATION_DISPLAY_ORDER:
        if name in groups:
            ordered[name] = groups[name]
    # Also include any unknown dep_types at the end
    for name, items in groups.items():
        if name not in ordered:
            ordered[name] = items
    return ordered


def _related_task_ids(item_id: int, rel_type: str | None = None) -> list[int]:
    """Return task IDs related to item_id, optionally narrowed by rel_type display name."""
    if rel_type is not None and rel_type not in CANONICAL_DEP_TYPES:
        valid = ", ".join(CANONICAL_DEP_TYPES)
        raise click.ClickException(
            f"Invalid relation type '{rel_type}'. Valid: {valid}"
        )

    if rel_type is None:
        rows = db.query(
            "SELECT source_id, target_id FROM task_deps "
            "WHERE source_type = 'task' AND target_type = 'task' "
            "AND (source_id = ? OR target_id = ?)",
            (item_id, item_id),
        )
    else:
        stored, swap = CANONICAL_DEP_TYPES[rel_type]
        # When swap=False, item_id should be the source side (we want the targets).
        # When swap=True, item_id should be the target side (we want the sources).
        if swap:
            rows = db.query(
                "SELECT source_id, target_id FROM task_deps "
                "WHERE source_type = 'task' AND target_type = 'task' "
                "AND target_id = ? AND dep_type = ?",
                (item_id, stored),
            )
        else:
            rows = db.query(
                "SELECT source_id, target_id FROM task_deps "
                "WHERE source_type = 'task' AND target_type = 'task' "
                "AND source_id = ? AND dep_type = ?",
                (item_id, stored),
            )

    ids: set[int] = set()
    for r in rows:
        if r["source_id"] != item_id:
            ids.add(r["source_id"])
        if r["target_id"] != item_id:
            ids.add(r["target_id"])
    return sorted(ids)


def show_relations(item_id: int):
    """Show all typed relations for a task, grouped by display-name."""
    if not db.exists("SELECT 1 FROM tasks WHERE id = ?", (item_id,)):
        raise click.ClickException(f"Task {task_id_display(item_id)} not found.")

    relations = get_all_relations(item_id)

    label = lambda s: click.style(s, fg="cyan")
    terminal = ("confirmed", "assumed", "declined", "obsolete")

    click.echo()
    click.echo(click.style(f"Relations for {task_id_display(item_id)}", fg="green", bold=True))
    click.echo(click.style("─" * 30, dim=True))

    if not relations:
        click.echo("  (none)")
        click.echo()
        return

    for display, items in relations.items():
        heading = RELATION_LABELS.get(display, display) + ":"
        click.echo(label(heading))
        for it in items:
            color = "green" if it["status"] in terminal else "yellow"
            click.echo(
                f"  {task_id_display(it['id'])} "
                f"[{click.style(it['status'], fg=color)}] "
                f"{it['title']}"
            )
    click.echo()
