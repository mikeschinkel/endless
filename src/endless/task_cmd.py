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


# Task relation vocabulary (E-957/E-958; informs dropped per E-1003;
# documents added per E-1007).
# display_name -> (stored_dep_type, swap_source_target)
# Stored types are active voice (source is the actor): blocks, implements,
# replaces, documents, cleans_up, reverses, modifies, relates_to. Inverse
# views (blocked_by, implemented_by, etc.) resolve to the same stored row
# queried with source/target swapped. The reverses/modifies pair (E-1156)
# are decision-to-decision relations; the others are task-to-task or task-
# to-decision.
CANONICAL_DEP_TYPES: dict[str, tuple[str, bool]] = {
    "blocks":          ("blocks",     False),  # source blocks target
    "blocked_by":      ("blocks",     True),   # inverse view
    "implements":      ("implements", False),  # source implements target
    "implemented_by":  ("implements", True),
    "replaces":        ("replaces",   False),  # source replaces target
    "replaced_by":     ("replaces",   True),
    "documents":       ("documents",  False),  # source documents target (records rationale for)
    "documented_by":   ("documents",  True),
    "cleans_up":       ("cleans_up",  False),  # source is post-ship cleanup of target; target does not wait
    "cleaned_up_by":   ("cleans_up",  True),
    "reverses":        ("reverses",   False),  # source reverses target (decision↔decision; target no longer in effect)
    "reversed_by":     ("reverses",   True),
    "modifies":        ("modifies",   False),  # source modifies target (decision↔decision; target partially still in effect)
    "modified_by":     ("modifies",   True),
    "relates_to":      ("relates_to", False),  # symmetric
}

# The 8 canonical stored types (the values in CANONICAL_DEP_TYPES, deduplicated).
STORED_DEP_TYPES = (
    "blocks", "implements", "replaces", "documents",
    "cleans_up", "reverses", "modifies", "relates_to",
)

# Display order for `task show` — actionability descending; symmetric last.
RELATION_DISPLAY_ORDER = (
    "blocked_by", "blocks",
    "implements", "implemented_by",
    "replaces",   "replaced_by",
    "reverses",   "reversed_by",
    "modifies",   "modified_by",
    "documents",  "documented_by",
    "cleans_up",  "cleaned_up_by",
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
    "reverses":       "Reverses",
    "reversed_by":    "Reversed by",
    "modifies":       "Modifies",
    "modified_by":    "Modified by",
    "documents":      "Documents",
    "documented_by":  "Documented by",
    "cleans_up":      "Cleans up",
    "cleaned_up_by":  "Cleaned up by",
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


# Field labels for change output emitted by state-mutating commands (E-1120).
_FIELD_LABELS = {
    "status":      "Status",
    "phase":       "Phase",
    "title":       "Title",
    "description": "Description",
    "text":        "Text",
    "parent_id":   "Parent",
    "tier":        "Tier",
    "outcome":     "Outcome",
}


_CHANGE_TRUNC_LEN = 40  # max chars for title/description/outcome in change output


def _truncate(s: str, n: int = _CHANGE_TRUNC_LEN) -> str:
    """Truncate a string to n chars, adding ellipsis if needed."""
    return s if len(s) <= n else s[: n - 3] + "..."


def _format_field_value(name: str, value) -> str:
    """Format a field value for change-output display ('<old> -> <new>')."""
    if value is None:
        return "∅"
    if name == "tier":
        try:
            return _TIER_LABELS.get(int(value), str(value))
        except (TypeError, ValueError):
            return str(value)
    if name == "parent_id":
        try:
            return task_id_display(int(value))
        except (TypeError, ValueError):
            return str(value)
    if name == "text":
        return "<set>" if value else "<cleared>"
    if name in ("title", "description", "outcome"):
        s = str(value)
        if not s:
            return "∅"
        return _truncate(s)
    return str(value)


def _emit_field_changes(
    item_id: int,
    title: str | None,
    changes: list,
    suffix: str | None = None,
):
    """Print 'Updated E-NNN (<title>): ' header and one '• <Label>: <old> -> <new>' bullet per change.

    `changes` is a list of (field_name, old_value, new_value) tuples.
    `suffix`, if given, is appended as a parenthesized note on the header line
    before the colon (e.g., '(cascaded to 3 descendants)').
    """
    bullet = click.style("•", fg="cyan")
    header = f"Updated {task_id_display(item_id)}"
    if title:
        header += f" ({_truncate(title)})"
    if suffix:
        header += f" {suffix}"
    header += ":"
    click.echo(header)
    for name, old, new in changes:
        label = _FIELD_LABELS.get(name, name)
        click.echo(f"{bullet} {label}: {_format_field_value(name, old)} -> {_format_field_value(name, new)}")


def _running_under_agent() -> bool:
    """True if invoked from an LLM agent harness.

    Today: Claude Code (sets CLAUDECODE=1). Extend as other harnesses are
    encountered. Used only to surface a stronger anti-rationalization variant
    of the verb-gate error — never to gate behavior.
    """
    import os
    return os.environ.get("CLAUDECODE") == "1"


_VERB_CHECK_PROMPT_TEMPLATE = (
    "Is '{word}' a verb? "
    "If it is say 'YES:' and then provide a definition. "
    "Your definition should be a single statement that does NOT include the "
    "verb in the statement. "
    "If not, just reply 'NO'"
)


def _check_verb_via_haiku(word: str) -> tuple[bool, str | None]:
    """Ask claude haiku whether `word` is a verb (E-1264).

    Invokes the `claude` binary directly via PATH lookup — no shell, no alias
    expansion, so users' `claude` shell wrappers are bypassed.

    Returns (True, definition) only on a clean `YES: <text>` reply with a
    non-empty definition. Every other outcome — `NO`, malformed output,
    timeout, missing binary, non-zero exit — returns (False, None), causing
    the caller to fall through to the standard verb-rejection error.
    """
    import subprocess
    from endless import internal_claude
    prompt = _VERB_CHECK_PROMPT_TEMPLATE.format(word=word)
    try:
        # Hook-suppressed (E-1470): a bare `claude -p` here inherits the
        # caller's TMUX_PANE and false-ends the live caller's session.
        result = internal_claude.run_internal_claude(
            prompt, model="haiku", timeout=30,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False, None
    if result.returncode != 0:
        return False, None
    response = result.stdout.strip()
    if not response.startswith("YES:"):
        return False, None
    definition = response[len("YES:"):].strip()
    if not definition:
        return False, None
    return True, definition


TITLE_MAX_LENGTH = 100


def validate_title(title: str, force: bool = False):
    """Reject titles that don't start with a registered actionable verb.

    On a miss, ask claude haiku whether the first word is a verb; if YES,
    auto-register it (E-1264) and let the title pass. NO / failure falls
    through to the standard error.

    Also reject titles longer than TITLE_MAX_LENGTH (E-1517). Length is a
    structural constraint, not a heuristic — `force` does NOT bypass it.

    Add new verbs manually with: endless verb add <new-verb> --definition "<def>"
    """
    if len(title) > TITLE_MAX_LENGTH:
        click.echo("", err=True)
        raise click.ClickException(
            f"Title is {len(title)} characters; max is {TITLE_MAX_LENGTH}.\n"
            f"\n"
            f"If it does not fit in {TITLE_MAX_LENGTH} chars, the title is usually naming HOW instead\n"
            f"of WHAT. Long-form belongs elsewhere: analysis in --analysis, design/plan in\n"
            f"--text, a brief blurb in --description — not the title.\n"
            f"\n"
            f"Consider using this template:\n"
            f"\n"
            f"    Shape: <verb> <subject>'s <symptom> on/when <trigger> [via <mechanism>]\n"
            f"    Subject   = user-facing name (e.g. 'just land'), not internal symbol\n"
            f"    Symptom   = what the user observes breaking (e.g. 'recording failure'),\n"
            f"                not the implementation cause\n"
            f"    Trigger   = when the symptom shows up (e.g. 'on self-modifying branches')\n"
            f"    Mechanism = optional; the flag/verb that fixes it (e.g. 'via --no-record').\n"
            f"                Include only when it sharpens understanding.\n"
        )
    first_word = title.split()[0].lower() if title.strip() else ""
    from endless import matchers
    verbs = matchers.get_verbs()
    if first_word in verbs:
        return
    if force:
        return

    # E-1264: ask claude haiku whether the first word is a verb. If YES,
    # auto-register it and let the title pass. This removes the agent's
    # bypass option (rewriting the title with a different verb) that was
    # wasting tokens across sessions. NO / failure paths fall through to
    # the standard error below.
    is_verb, definition = _check_verb_via_haiku(first_word)
    if is_verb and definition:
        try:
            matchers.add_verb(value=first_word, definition=definition)
        except ValueError:
            pass
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" Auto-registered verb '{first_word}': {definition}"
            )
            return

    register_cmd = (
        f"endless verb add '{first_word}' --definition \"<short definition>\""
    )

    if _running_under_agent():
        msg = (
            f"Title must start with an actionable verb. '{first_word}' is not registered.\n"
            f"\n"
            f"  Decide: does '{first_word}' name an action?\n"
            f"  (Can a person DO it? 'consider' yes. 'maybe' no.)\n"
            f"\n"
            f"  IF YES:  {register_cmd}\n"
            f"  IF NO:   rewrite the title with a verb.\n"
            f"           Do not register a non-verb to bypass this gate.\n"
            f"\n"
            f"  Registering a non-verb defeats the check for everyone — including future-you."
        )
    else:
        msg = (
            f"Title must start with an actionable verb. '{first_word}' is not registered.\n"
            f"  Register it (if it really is a verb): {register_cmd}"
        )
    raise click.ClickException(msg)


DESCRIPTION_MAX_LENGTH = 1024


def validate_description(description: str | None):
    """Reject descriptions longer than 1024 chars or with embedded newlines.

    Per E-1058 / E-1073: description is a 2-3 sentence blurb, not long-form.
    Empty or None is allowed here; required-ness is E-963's concern.
    """
    if not description:
        return
    if len(description) > DESCRIPTION_MAX_LENGTH:
        raise click.ClickException(
            f"Description is {len(description)} characters; max is {DESCRIPTION_MAX_LENGTH}.\n"
            f"  Description is a 2-3 sentence blurb, not a dissertation. Long-form context\n"
            f"  belongs in a dedicated field: analysis in --analysis, plans/verification in --text."
        )
    if "\n" in description or "\r" in description:
        raise click.ClickException(
            "Description must be a single line; embedded newlines are not allowed.\n"
            "  Description is a brief blurb. Long-form context belongs in a dedicated field:\n"
            "  analysis in --analysis, plans/verification in --text."
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


def _main_root_for_task(task_id: int) -> Path | None:
    """Return the registered main-checkout root of the project that owns this task."""
    row = db.query(
        "SELECT p.path FROM projects p "
        "JOIN tasks t ON t.project_id = p.id "
        "WHERE t.id = ? LIMIT 1",
        (task_id,),
    )
    if not row:
        return None
    return Path(row[0]["path"]).expanduser().resolve()


def _worktree_for_task(task_id: int) -> Path | None:
    """Return the worktree Path for a task if one exists, else None.

    E-1216: plan files for a task live in its worktree, not in main. This is
    the canonical "where do plan-file writes go?" resolver. Lookup is by
    deterministic path (<main>/.endless/worktrees/e-<id>/), with the
    `.endless/worktree.json` companion's mere presence (not its task_id
    field) acting as the "endless-managed marker" per E-1301.
    """
    from endless.worktree_cmd import (
        _task_id_from_worktree_path,
        _warn_if_companion_disagrees,
        _read_companion,
    )
    main_root = _main_root_for_task(task_id)
    if main_root is None:
        return None
    wt_dir = main_root / ".endless" / "worktrees" / f"e-{task_id}"
    companion_path = wt_dir / ".endless" / "worktree.json"
    if not wt_dir.is_dir() or not companion_path.exists():
        return None
    if _task_id_from_worktree_path(wt_dir) != f"E-{task_id}":
        return None
    companion = _read_companion(wt_dir)
    _warn_if_companion_disagrees(wt_dir, companion)
    return wt_dir


def _display_path(p: Path) -> str:
    """Display a Path with $HOME collapsed to ~."""
    s = str(p)
    home = str(Path.home())
    return s.replace(home, "~", 1) if s.startswith(home) else s


def _mirror_plan_to_worktree(task_id: int, content: str) -> Path | None:
    """Mirror plan text into the task's worktree IF one exists (E-1445).

    The DB's `tasks.text` column is the source of truth and is written
    separately via the task event payload. This only mirrors that content to
    `<worktree>/.endless/plans/E-NNN.md` as a convenience when a worktree
    already exists.

    It NEVER creates a worktree (rescinds the E-1216 auto-create default,
    which surprised callers by provisioning worktrees + sandboxes for tasks
    they had no intention of working on yet). When no worktree exists, the DB
    is updated and nothing is written to disk; the plan file materializes
    later when the worktree is born at claim/spawn
    (`worktree_cmd.create_task_worktree` → `_materialize_plan_file`).

    Returns the written path, or None when no worktree exists.
    """
    wt_path = _worktree_for_task(task_id)
    if wt_path is None:
        return None

    plans_dir = wt_path / ".endless" / "plans"
    plans_dir.mkdir(parents=True, exist_ok=True)
    target = plans_dir / f"E-{task_id}.md"
    target.write_text(content)
    click.echo(
        click.style("✓", fg="green")
        + f" Wrote plan to {_display_path(target)}"
    )
    from endless.worktree_cmd import _commit_plan_file_in_worktree
    _commit_plan_file_in_worktree(
        wt_path, task_id, f"Endless: update plan for E-{task_id}",
    )
    return target


def _resolve_project(name: str | None) -> tuple[int, str]:
    """Resolve project name, return (id, name)."""
    if not name:
        # Under --db main inside a worktree, walk to the main checkout
        # so cwd-keyed lookups find the canonical project row instead of
        # the worktree's path. See config.resolution_cwd.
        cwd = config.resolution_cwd()
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
    # Order matters: substring matching, first hit wins. Keep `urgent`
    # aliases ahead of everything else so a heading like "Urgent now"
    # maps to `urgent` rather than `now`.
    phase_map = {
        "urgent": "urgent",
        "asap": "urgent",
        "five-alarm": "urgent",
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
        "maybe": "maybe",
        "considering": "maybe",
        "tentative": "maybe",
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

    where = "WHERE pi.project_id = ?"
    params: list = [project_id]
    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND pi.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += " AND pi.status NOT IN ('confirmed', 'assumed', 'completed', 'declined', 'obsolete')"
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
        "phase": "CASE pi.phase WHEN 'urgent' THEN 0 WHEN 'now' THEN 1 WHEN 'next' THEN 2 WHEN 'later' THEN 3 WHEN 'maybe' THEN 4 ELSE 5 END",
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
            "completed": click.style("◆", fg="green"),
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
        "WHERE t.status NOT IN ('confirmed', 'assumed', 'completed', 'blocked', 'declined', 'obsolete', 'in_progress', 'verify') "
        "AND (SELECT count(*) FROM tasks c WHERE c.parent_id = t.id) = 0 "
        "AND t.id NOT IN ("
        "  SELECT td.target_id FROM task_deps td"
        "  WHERE td.target_type = 'task' AND td.dep_type = 'blocks'"
        "    AND td.source_id IN ("
        "      SELECT t2.id FROM tasks t2 "
        "      WHERE t2.status NOT IN ('confirmed', 'assumed', 'completed')"
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
        f"    WHEN 'urgent' THEN 0 WHEN 'now' THEN 1 WHEN 'next' THEN 2 "
        f"    WHEN 'later' THEN 3 WHEN 'maybe' THEN 4 ELSE 5 END, "
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


def _format_relative(iso_ts: str | None) -> str:
    """Render a UTC ISO-8601 timestamp ('2026-05-26T12:00:00') as a coarse
    relative age: '<n>s ago' / '<n>m ago' / '<n>h ago' / '<n>d ago'."""
    if not iso_ts:
        return "an unknown time ago"
    try:
        dt = datetime.strptime(iso_ts, "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc)
    except ValueError:
        return iso_ts
    delta = max(0, int((datetime.now(timezone.utc) - dt).total_seconds()))
    if delta < 60:
        return f"{delta}s ago"
    if delta < 3600:
        return f"{delta // 60}m ago"
    if delta < 86400:
        return f"{delta // 3600}h ago"
    return f"{delta // 86400}d ago"


def revise_next_list(
    file_path: str,
    project_name: str | None = None,
    as_json: bool = False,
):
    """Replace a project's curated 'next' list from a JSON file (full rewrite).

    Schema validation, the soft/hard item caps, the cross-session collision read,
    and the BEGIN IMMEDIATE transaction all live in the Go event executor
    (`endless-go event`). This reads the file, parses it (rejecting malformed JSON),
    and emits the `project_next.revised` event. On success it prints the
    collision notice, any soft-cap warning, then either the resulting JSON
    (`--json`) or a one-line human summary.
    """
    import json

    _, name = _resolve_project(project_name)

    p = Path(file_path).expanduser()
    if not p.exists():
        raise click.ClickException(f"File not found: {p}")
    try:
        data = json.loads(p.read_text())
    except json.JSONDecodeError as e:
        raise click.ClickException(f"Invalid JSON in {p}: {e}")

    from endless.event_bridge import emit_event

    result = emit_event(
        kind="project_next.revised",
        project=name,
        entity_type="project_next",
        entity_id=name,
        payload=data,
        prompt_verb="revised for",
    ) or {}

    # Collision visibility: the prior revision was read inside the Go
    # transaction and returned here (E-894 keeps the read in Go).
    prior = result.get("prior_revision")
    if prior:
        collision = (
            f"list last revised {_format_relative(prior.get('revised_at'))} "
            f"by session {prior.get('session_id')}"
        )
    else:
        collision = "first revision"

    warning = result.get("warning")
    state = result.get("state") or {}

    if as_json:
        # Keep stdout pure JSON for piping; advisory lines go to stderr.
        click.echo(collision, err=True)
        if warning:
            click.echo(f"warning: {warning}", err=True)
        click.echo(json.dumps(state, indent=2))
    else:
        click.echo(collision)
        if warning:
            click.echo(f"warning: {warning}", err=True)
        lanes = state.get("lanes") or []
        item_count = sum(len(lane.get("items") or []) for lane in lanes)
        click.echo(f"Revised: {len(lanes)} lane(s), {item_count} item(s).")


def active_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    llm: bool = False,
    as_json: bool = False,
    parent_id: int | None = None,
):
    """Show tasks that are in progress or awaiting verification."""
    where = "WHERE t.status IN ('in_progress', 'verify')"
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
    where = "WHERE 1=1"
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


def _render_landed_table(rows):
    """Render landed-task rows: ID, Landed, Lands, Title (E-1478)."""
    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 80

    id_w = max(2, max(len(task_id_display(r["id"])) for r in rows))
    landed_strs = [_format_timestamp(r["last_landed"]) for r in rows]
    la_w = max(6, max(len(s) for s in landed_strs))
    cnt_strs = [str(r["land_count"]) for r in rows]
    cn_w = max(5, max(len(s) for s in cnt_strs))
    gap = "  "
    fixed_width = id_w + la_w + cn_w + len(gap) * 3
    title_width = max(20, term_width - fixed_width)

    display_titles = []
    for r in rows:
        title = r["title"]
        if len(title) > title_width:
            title = title[:title_width - 1] + "…"
        display_titles.append(title)
    max_title_len = max(len(t) for t in display_titles) if display_titles else 5

    header = (f"{'ID':<{id_w}}{gap}{'Landed':<{la_w}}{gap}"
              f"{'Lands':<{cn_w}}{gap}Title")
    sep = f"{'─'*id_w}{gap}{'─'*la_w}{gap}{'─'*cn_w}{gap}{'─'*max_title_len}"
    click.echo(header)
    click.echo(sep)
    for r, landed, cnt, title in zip(rows, landed_strs, cnt_strs, display_titles):
        click.echo(
            f"{task_id_display(r['id']):<{id_w}}{gap}"
            f"{landed:<{la_w}}{gap}{cnt:<{cn_w}}{gap}{title}"
        )


def landed_list(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int = 20,
    llm: bool = False,
    as_json: bool = False,
):
    """List tasks that have landed at least once, most-recent landing first (E-1478)."""
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
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) AS title, "
        f"t.status, t.tier, p.name AS project_name, "
        f"MAX(l.landed_at) AS last_landed, COUNT(l.id) AS land_count "
        f"FROM task_landings l "
        f"JOIN tasks t ON t.id = l.task_id "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"GROUP BY t.id "
        f"ORDER BY last_landed DESC "
        f"LIMIT ?",
        tuple(params),
    )

    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo("# no landed tasks")
        else:
            click.echo(click.style("•", fg="cyan") + " No landed tasks")
        return

    if as_json:
        import json
        out = [
            {
                "id": f"E-{r['id']}",
                "phase": r["phase"],
                "status": r["status"],
                "title": r["title"],
                "project": r["project_name"],
                "last_landed": r["last_landed"],
                "count": r["land_count"],
            }
            for r in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    # Group by project
    groups: dict[str, list] = {}
    for r in rows:
        groups.setdefault(r["project_name"], []).append(r)

    for proj, items in groups.items():
        if llm:
            click.echo(f"# {proj}")
            for r in items:
                suffix = f" x{r['land_count']}" if r["land_count"] > 1 else ""
                click.echo(
                    f"E-{r['id']} {_format_timestamp(r['last_landed'])}{suffix} "
                    f"{r['status']} {r['title']}"
                )
        else:
            click.echo()
            click.echo(click.style(f"Landed ({proj}):", bold=True))
            _render_landed_table(items)
    if not llm:
        click.echo()


def landed_item(item_id: int, llm: bool = False, as_json: bool = False):
    """Show the full landing history for a single task, newest first (E-1478)."""
    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) AS title, "
        "p.name AS project_name "
        "FROM tasks t JOIN projects p ON t.project_id = p.id "
        "WHERE t.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(f"No task found with id {item_id}")
    item = row[0]
    landings = _task_landings(item_id)

    if as_json:
        import json
        out = {
            "id": f"E-{item['id']}",
            "title": item["title"],
            "project": item["project_name"],
            "landings": [
                {
                    "landed_at": land["landed_at"],
                    "merge_commit_sha": land["merge_commit_sha"],
                    "branch": land["branch"],
                }
                for land in landings
            ],
        }
        click.echo(json.dumps(out, indent=2))
        return

    if llm:
        click.echo(f"# E-{item['id']} {item['title']}")
        if not landings:
            click.echo("# never landed")
            return
        for land in landings:
            sha = (land["merge_commit_sha"] or "")[:7]
            click.echo(f"{land['landed_at']} {sha} {land['branch']}")
        return

    # Human-readable
    click.echo()
    click.echo(click.style(
        f"Landings for {task_id_display(item['id'])} ({item['title']}):",
        bold=True))
    if not landings:
        click.echo(click.style("•", fg="cyan") + " Never landed")
        click.echo()
        return
    for land in landings:
        sha = (land["merge_commit_sha"] or "")[:7]
        ts = _format_timestamp(land["landed_at"])
        click.echo(f"  {ts}  {sha}  {land['branch']}")
    click.echo()


# E-1544: research-gate helpers. ED-1504 requires `--type research` to be
# justified unless `--parent` is a type=epic, status=in_progress task.

_RESEARCH_GATE_MSG = (
    "--type research requires --justification explaining why the "
    "research can't be inline in a do-task."
)

_JUSTIFICATION_HEADING_RE = re.compile(r"(?m)^##\s+Justification\b")


def _research_gate_check(parent_id: int | None, justification: str | None) -> None:
    """Raise click.ClickException if the research gate fails.

    Pass when (a) `justification` is non-empty, or (b) parent is a
    type=epic, status=in_progress task. Sticky-override statuses
    (revisit/blocked/declined/obsolete) do NOT exempt.
    """
    if justification:
        return
    if parent_id is None:
        raise click.ClickException(_RESEARCH_GATE_MSG)
    row = db.query(
        "SELECT t.status, COALESCE(tt.slug, '') AS type_slug "
        "FROM tasks t LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE t.id = ?",
        (parent_id,),
    )
    if not row:
        raise click.ClickException(
            f"Parent task E-{parent_id} not found"
        )
    if row[0]["type_slug"] != "epic" or row[0]["status"] != "in_progress":
        raise click.ClickException(_RESEARCH_GATE_MSG)


def _compose_justification_notes(
    existing_notes: str | None,
    justification: str | None,
) -> str | None:
    """Return notes-string with a '## Justification' section appended.

    - If `justification` is empty/None, returns None (no notes change).
    - If existing notes already contains a '## Justification' heading,
      raises click.ClickException (no overwrite — collision is loud).
    - Otherwise appends `## Justification\\n\\n<text>\\n`, preserving any
      existing content.
    """
    if not justification:
        return None
    section = "## Justification\n\n" + justification.strip() + "\n"
    if existing_notes and _JUSTIFICATION_HEADING_RE.search(existing_notes):
        raise click.ClickException(
            "notes already contains a '## Justification' section; "
            "clear or edit it manually before re-justifying."
        )
    if not existing_notes:
        return section
    return existing_notes.rstrip() + "\n\n" + section


def add_item(
    title: str,
    description: str | None = None,
    text_file: str | None = None,
    phase: str = "now",
    project_name: str | None = None,
    after: int | None = None,
    parent_id: int | None = None,
    task_type: str | None = None,
    status: str | None = None,
    tier: int | None = None,
    force: bool = False,
    justification: str | None = None,
):
    """Add a single task."""
    from endless.event_bridge import emit_event

    task_type = task_type or "task"
    validate_title(title, force=force)
    validate_description(description)
    _, proj_name = _resolve_project(project_name)
    status = status or ("ready" if tier == 1 else "needs_plan")

    # E-1577: research/epic tasks cannot be created in 'assumed'/'confirmed'.
    _require_terminal_allowed_for_type(status, task_type)

    # E-1544: research-gate. Justification (when present) accepted-and-stored
    # even if parent is epic+in_progress (gate only governs *requiring* it).
    if task_type == "research":
        _research_gate_check(parent_id, justification)
    notes_value = _compose_justification_notes(None, justification)

    text_content: str | None = None
    if text_file is not None:
        p = Path(text_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        text_content = p.read_text()

    payload = {
        "title": title,
        "description": description or "",
        "phase": phase,
        "status": status,
        "type": task_type,
    }
    if text_content is not None:
        payload["text"] = text_content
    if notes_value is not None:
        payload["notes"] = notes_value
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
    if text_content is not None:
        _mirror_plan_to_worktree(item_id, text_content)
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


def _require_outcome_for_declined(status: str | None, outcome: str | None):
    if status == "declined" and not (outcome and outcome.strip()):
        raise click.ClickException(
            "An outcome is required when declining a task. "
            "Use --reason (on `task decline`) or --outcome to explain why."
        )


def _lead_verb(title: str | None) -> str:
    """Return the lowercased first whitespace-delimited word of `title`, with
    surrounding punctuation stripped. Returns '' if title is empty or missing."""
    if not title:
        return ""
    first = title.strip().split(None, 1)[0] if title.strip() else ""
    return first.strip(".,:;!?\"'()[]{}").lower()


def _require_completable_verb_for_completed(
    status: str | None,
    title: str | None,
):
    """E-1240: `completed` is gated to tasks whose title's lead verb is
    marked `completable: true` in verbs.json. Reserves the status for
    findings-as-deliverable work (audits, research, reviews, …) and keeps
    implementation tasks on the `verify`/`confirmed`/`assumed` track."""
    if status != "completed":
        return
    from endless.matchers import is_completable_verb
    verb = _lead_verb(title)
    if not is_completable_verb(verb):
        shown = verb or "(none)"
        raise click.ClickException(
            f"Status 'completed' requires a completable lead verb in the "
            f"task title. Title's lead verb is {shown!r}, which is not "
            f"marked `completable: true` in verbs.json.\n"
            f"Completable verbs (e.g. audit, research, investigate, review, "
            f"analyze) signal that the deliverable is text/findings, not "
            f"behavior. For implementation tasks, use 'verify' → 'confirmed' "
            f"or 'assumed' instead."
        )


def _require_outcome_for_completed(status: str | None, outcome: str | None):
    """E-1240: `completed` requires --outcome because the outcome text IS
    the deliverable for findings-style tasks."""
    if status == "completed" and not (outcome and outcome.strip()):
        raise click.ClickException(
            "An outcome is required when completing a task. "
            "The outcome captures the findings/deliverable — use --outcome "
            "to provide it. (For implementation tasks where behavior is "
            "the deliverable, use 'verify' → 'confirmed' / 'assumed' instead.)"
        )


_TYPE_REJECTS_ASSUMED_CONFIRMED = ("research", "epic")


def _require_terminal_allowed_for_type(status: str | None, task_type: str | None):
    """E-1577: research and epic tasks reject 'assumed'/'confirmed' — their
    only type-specific terminal is 'completed' (per E-1537 §3). Universal
    terminals 'obsolete' and 'declined' remain allowed for all types."""
    if status in ("assumed", "confirmed") and task_type in _TYPE_REJECTS_ASSUMED_CONFIRMED:
        raise click.ClickException(
            f"Task type {task_type!r} cannot use status {status!r}. "
            f"Use --status completed (or `endless task complete`) with --outcome."
        )


def _refuse_cascade_across_typed_descendants(item_id: int, status: str):
    """E-1577: when --cascade would set 'assumed'/'confirmed' on a subtree,
    refuse loudly if any descendant is research/epic. Naming offenders
    matches the 'loud failure on invalid state' rule."""
    if status not in ("assumed", "confirmed"):
        return
    offenders = db.query(
        "WITH RECURSIVE tree(id) AS ("
        "  SELECT id FROM tasks WHERE id = ?"
        "  UNION ALL"
        "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
        ") "
        "SELECT t.id, COALESCE(t.title, t.description) AS title, "
        "       COALESCE(tt.slug, '') AS type "
        "FROM   tasks t "
        "LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE  t.id IN (SELECT id FROM tree) "
        "  AND  COALESCE(tt.slug, '') IN ('research', 'epic') "
        "  AND  t.id != ?",
        (item_id, item_id),
    )
    if offenders:
        lines = ", ".join(
            f"{task_id_display(r['id'])} ({r['type']})" for r in offenders
        )
        raise click.ClickException(
            f"Cannot cascade status {status!r}: subtree contains "
            f"research/epic descendant(s) that reject this terminal: {lines}. "
            f"Handle those separately with --status completed."
        )


def complete_item(item_id: int, cascade: bool = False, outcome: str | None = None):
    """Mark a task as confirmed."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) as title, t.status, "
        "       COALESCE(tt.slug, '') AS type "
        "FROM   tasks t "
        "LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE  t.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    _require_terminal_allowed_for_type("confirmed", row[0]["type"])
    if cascade:
        _refuse_cascade_across_typed_descendants(item_id, "confirmed")

    if row[0]["status"] == "confirmed" and not cascade:
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already confirmed"
        )
        return

    _, proj_name = _resolve_project(None)
    payload = {
        "old_status": row[0]["status"],
        "new_status": "confirmed",
        "cascade": cascade,
    }
    if outcome:
        payload["outcome"] = outcome
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload=payload,
    )

    changes = [("status", row[0]["status"], "confirmed")]
    if outcome:
        changes.append(("outcome", None, outcome))
    suffix = None
    if cascade:
        count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 1
        suffix = f"(cascaded to {count - 1} descendant(s))"
    _emit_field_changes(item_id, row[0]["title"], changes, suffix=suffix)


def assume_item(item_id: int, cascade: bool = False, outcome: str | None = None):
    """Mark a task as assumed (believed complete, not yet verified)."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) as title, t.status, "
        "       COALESCE(tt.slug, '') AS type "
        "FROM   tasks t "
        "LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE  t.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    _require_terminal_allowed_for_type("assumed", row[0]["type"])
    if cascade:
        _refuse_cascade_across_typed_descendants(item_id, "assumed")

    if row[0]["status"] == "assumed" and not cascade:
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already assumed"
        )
        return

    _, proj_name = _resolve_project(None)
    payload = {
        "old_status": row[0]["status"],
        "new_status": "assumed",
        "cascade": cascade,
    }
    if outcome:
        payload["outcome"] = outcome
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload=payload,
    )

    changes = [("status", row[0]["status"], "assumed")]
    if outcome:
        changes.append(("outcome", None, outcome))
    suffix = None
    if cascade:
        count = db.scalar(
            "WITH RECURSIVE tree(id) AS ("
            "  SELECT id FROM tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 1
        suffix = f"(cascaded to {count - 1} descendant(s))"
    _emit_field_changes(item_id, row[0]["title"], changes, suffix=suffix)


def mark_completed_item(item_id: int, outcome: str):
    """E-1240: Mark a findings-as-deliverable task as `completed`.

    Gated by `--outcome` (required) and by `completable: true` on the
    task title's lead verb in verbs.json. Distinct from `confirmed`
    (behavior verified) and `assumed` (behavior believed correct,
    awaiting promotion). Use for Audit/Research/Investigate/Review-style
    tasks whose deliverable is the outcome text itself."""
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

    _require_outcome_for_completed("completed", outcome)
    _require_completable_verb_for_completed("completed", row[0]["title"])

    if row[0]["status"] == "completed":
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already completed"
        )
        return

    _, proj_name = _resolve_project(None)
    payload = {
        "old_status": row[0]["status"],
        "new_status": "completed",
        "outcome": outcome,
    }
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload=payload,
    )

    changes = [
        ("status", row[0]["status"], "completed"),
        ("outcome", None, outcome),
    ]
    _emit_field_changes(item_id, row[0]["title"], changes)


def decline_item(item_id: int, reason: str):
    """Mark a task as declined; reason is required and stored as outcome."""
    from endless.event_bridge import emit_event

    _require_outcome_for_declined("declined", reason)

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    if row[0]["status"] == "declined":
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already declined"
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
            "new_status": "declined",
            "cascade": False,
            "outcome": reason,
        },
    )

    changes = [
        ("status", row[0]["status"], "declined"),
        ("outcome", None, reason),
    ]
    _emit_field_changes(item_id, row[0]["title"], changes)


def _eswt_defined_in_user_shell() -> bool:
    """Probe the user's interactive shell for the 'eswt' function.

    Functions defined by 'endless shell-init' live in the parent shell's
    process memory and don't propagate to Python subprocesses. To check
    them, spawn $SHELL -ic which sources the user's rc files (where the
    shell-init snippet was eval'd), then run 'command -v eswt'.

    Returns False on any failure so output defaults to the bootstrap
    form — better to over-instruct than to print a command the user's
    shell can't actually run.
    """
    import subprocess

    shell = os.environ.get("SHELL")
    if not shell:
        return False
    try:
        result = subprocess.run(
            [shell, "-ic", "command -v eswt >/dev/null"],
            capture_output=True,
            timeout=3,
        )
        return result.returncode == 0
    except (subprocess.TimeoutExpired, OSError, ValueError):
        return False


def _current_session_active_task_id() -> int | None:
    """The active task id of the current Endless session, if any.

    Fills the spawn handoff's "Spawning session: E-NNNN" origin line (E-1469).
    Returns None when there is no resolvable current session or it has no
    active task.
    """
    eid = _current_endless_session_id()
    if eid is None:
        return None
    rows = db.query(
        "SELECT active_task_id FROM sessions WHERE id = ?",
        (eid,),
    )
    if not rows or rows[0]["active_task_id"] is None:
        return None
    return rows[0]["active_task_id"]


def _current_endless_session_id() -> int | None:
    """Best-effort lookup of the current Endless session id (int PK).

    Four-layer resolution:
      1. ENDLESS_SESSION_ID env var (digit form) — explicit caller override.
      2. CLAUDECODE=1 + CLAUDE_CODE_SESSION_ID (E-1455) — env-vars-as-truth
         for current-pane identification. The current process IS the Claude
         pane; identity is in-process. Resolves (and lazy-INSERTs on first-
         event-timing race) via Go's `session-query ensure-claude-id`.
      3. TMUX_PANE-matching live session — used by shell panes whose env
         doesn't carry CLAUDECODE but whose pane id matches a DB-known
         Claude pane.
      4. Sibling Claude pane in the same tmux window, when there is
         EXACTLY ONE such sibling. Lets a shell pane in a Claude-using
         window transparently attribute commands to its sibling Claude
         session (E-1294, follow-up to E-1287). On 0 or 2+ sibling
         matches, returns None.

    For the n>1 case, callers that emit events should use
    `_resolve_session_id_with_prompt()` instead — it surfaces a list of
    candidate sessions and asks the user to pick. This entry point
    stays heuristic-free so background / non-interactive code paths
    can ask "is there an obvious session?" without ever prompting.

    Returns None when none of the layers resolve. Callers must treat
    None as "no current session".
    """
    env_id = os.environ.get("ENDLESS_SESSION_ID")
    if env_id and env_id.isdigit():
        return int(env_id)

    # Layer 2 (E-1455): env-vars-as-truth for current-pane Claude identity.
    # Avoids the first-event-timing race where the resolver runs before
    # the hook has registered the pane in the DB.
    if os.environ.get("CLAUDECODE") == "1":
        claude_session_id = os.environ.get("CLAUDE_CODE_SESSION_ID")
        if claude_session_id:
            eid = _ensure_claude_session_id(claude_session_id)
            if eid is not None:
                return eid

    pane = os.environ.get("TMUX_PANE")
    if not pane:
        return None
    from endless.session_cmd import _live_sessions, _project_root_for_cwd
    project_root = _project_root_for_cwd()
    live = _live_sessions(project_root)
    for c in live:
        if c.get("pane_id") == pane:
            eid = c.get("endless_session_id")
            if isinstance(eid, int):
                return eid

    # Sibling shell pane (no CLAUDECODE env): read the Claude session UUID
    # the sibling Claude session's hook published to the tmux window
    # (@endless_session_uuid, E-1585) and resolve/populate it in the active
    # DB context (the sandbox under --db sandbox). Unambiguous — one UUID per
    # window — so it runs before the heuristic n==1 sibling-DB lookup. process=""
    # so this shell's own pane is not recorded as the Claude session's process.
    window_uuid = _tmux_window_session_uuid()
    if window_uuid:
        eid = _ensure_claude_session_id(window_uuid, process="")
        if eid is not None:
            return eid

    # Pane-direct didn't match — try sibling Claude pane in the same
    # tmux window. Single-match only; 0 or 2+ falls through to None.
    sibling_eid, n = _find_sibling_claude_session()
    if n == 1 and sibling_eid is not None:
        return sibling_eid
    return None


def _tmux_window_session_uuid() -> str | None:
    """Read @endless_session_uuid from the current tmux window (E-1585).

    The sibling Claude session's hook publishes its CLAUDE_CODE_SESSION_ID
    to the window via this option; any pane in the window (including a plain
    shell with no Claude env) can read it. Returns the UUID string, or None
    when not in tmux or the option is unset/empty.
    """
    import subprocess

    pane = os.environ.get("TMUX_PANE")
    if not pane:
        return None
    try:
        result = subprocess.run(
            ["tmux", "display-message", "-p", "-t", pane,
             "#{@endless_session_uuid}"],
            capture_output=True, text=True, timeout=5,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    uuid = result.stdout.strip()
    return uuid or None


def _ensure_claude_session_id(
    claude_session_id: str, process: str | None = None
) -> int | None:
    """Resolve sessions.id for the env-identified Claude session (E-1455).

    Shells out to `endless-go session-query ensure-claude-id`, which
    composes TouchSession (idempotent INSERT-or-UPSERT with collision
    invalidation) and a follow-up id lookup. Lazy-creates the row when
    no hook event has fired yet — the first-event-timing race the env-
    var path exists to solve. Subsequent hook events upsert the same row
    idempotently, so this path produces the same row the hook would.

    `process` is the tmux pane to record for the session row. When None
    (the E-1455 env-vars-as-truth caller), the current pane is used —
    correct there because the calling process IS the Claude pane. The
    E-1585 window-option caller is a *sibling shell*, so it passes ""
    to avoid recording its own pane as the Claude session's process
    (which TouchSession's collision invalidation would then hijack); the
    row is resolved purely by UUID.

    Returns the integer id on success; None on any failure (caller falls
    through to the remaining resolver layers).
    """
    import subprocess
    from endless import config
    from endless.session_cmd import _project_root_for_cwd

    try:
        project_root = _project_root_for_cwd()
    except Exception:
        return None
    pane = process if process is not None else os.environ.get("TMUX_PANE", "")

    args = [
        "endless-go", *config.go_db_context_args(),
        "session-query", "ensure-claude-id",
        "--session-id", claude_session_id,
        "--project-root", str(project_root),
    ]
    if pane:
        args += ["--process", pane]

    try:
        result = subprocess.run(args, capture_output=True, text=True, timeout=5)
    except (FileNotFoundError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    text = result.stdout.strip()
    if not text.isdigit():
        return None
    return int(text)


def _list_sibling_claude_session_eids() -> list[int]:
    """Return the live Endless session ids in sibling tmux panes
    (same window as TMUX_PANE).

    Returns [] when not in tmux, when there are no sibling panes, or
    when no sibling pane has a live companion file. This is the
    candidate set that `_resolve_session_id_with_prompt` validates the
    user's prompt input against.
    """
    from endless.session_cmd import (
        _live_sessions,
        _project_root_for_cwd,
        _tmux_window_pane_ids,
    )
    pane_ids = _tmux_window_pane_ids()
    if not pane_ids:
        return []
    my_pane = os.environ.get("TMUX_PANE")
    sibling_panes = {p for p in pane_ids if p != my_pane}
    if not sibling_panes:
        return []
    project_root = _project_root_for_cwd()
    live = _live_sessions(project_root)
    return [
        c["endless_session_id"] for c in live
        if c.get("pane_id") in sibling_panes
        and isinstance(c.get("endless_session_id"), int)
    ]


def _find_sibling_claude_session() -> tuple[int | None, int]:
    """Find a live Claude session in a sibling tmux pane (same window).

    Cross-pane lookup only — a pane cannot read another pane's process env,
    so DB query is the right (and only) mechanism here. The current pane's
    own identity short-circuits in `_current_endless_session_id` via the
    CLAUDECODE/CLAUDE_CODE_SESSION_ID env vars (E-1455) before this
    sibling search runs. Precedence: same-pane → env vars; cross-pane → DB.

    Returns (session_eid, num_matches):
      - (None, 0) — no sibling Claude session (or not in tmux)
      - (eid, 1)  — exactly one match; bind to that session
      - (None, n) — n>1 matches; ambiguous (heuristic resolution refused)
    """
    eids = _list_sibling_claude_session_eids()
    if not eids:
        return None, 0
    if len(eids) > 1:
        return None, len(eids)
    return eids[0], 1


# Per-process cache so a single command that emits multiple events only
# prompts the user once. Reset between tests via the
# `_reset_session_choice_cache` helper at the bottom of this module.
_session_choice_cache: int | None = None


def _resolve_session_id_with_prompt(
    *,
    project_name: str | None = None,
    prompt_verb: str | None = None,
) -> int | None:
    """Resolve the current Endless session id, prompting on ambiguity.

    Layered like `_current_endless_session_id`:
      1. ENDLESS_SESSION_ID env var.
      2. TMUX_PANE-direct companion match.
      3. Single sibling Claude pane → auto-pick.

    If those fail AND there are n>1 sibling Claude panes alive in the
    current tmux window:
      - On a tty: display `endless session list --project <project>`
        and prompt for a session ID. The input is validated against
        the live sibling-pane candidate set.
      - On non-tty: raise `click.ClickException`. Claude-spawned
        commands inherit `ENDLESS_SESSION_ID` and never reach this
        branch; only humans running interactive commands from a shell
        pane do. Errors loudly so a misfire is recognizable.

    The chosen id is cached at module scope for the lifetime of the
    process; subsequent calls return it without re-prompting. Tests
    reset the cache via `_reset_session_choice_cache()`.

    `prompt_verb` shapes the question — e.g. "claimed for" yields
    "Which session should this be claimed for? [ID]:". When None,
    falls back to "associated with".
    """
    global _session_choice_cache
    if _session_choice_cache is not None:
        return _session_choice_cache

    eid = _current_endless_session_id()
    if eid is not None:
        _session_choice_cache = eid
        return eid

    candidate_eids = _list_sibling_claude_session_eids()
    if len(candidate_eids) <= 1:
        # 0 candidates: no fallback possible. 1 candidate: already auto-
        # picked above (layer 3) — only reachable if that path returned
        # None for some other reason (defensive).
        return None

    import sys
    n = len(candidate_eids)
    if not sys.stdin.isatty():
        raise click.ClickException(
            f"There are {n} live Claude sessions in this tmux window "
            f"and stdin is not a tty, so the session id cannot be "
            f"resolved interactively.\n"
            f"Set ENDLESS_SESSION_ID=<id> for this command, or run it "
            f"interactively to choose."
        )

    from endless.session_cmd import list_sessions
    click.echo("There are multiple Claude sessions in this tmux window:")
    click.echo("")
    list_sessions(project_name=project_name)
    click.echo("")
    verb = prompt_verb or "associated with"
    question = f"Which session should this be {verb}? [ID]"
    candidate_set = set(candidate_eids)
    while True:
        choice = click.prompt(question, type=int)
        if choice in candidate_set:
            _session_choice_cache = choice
            return choice
        click.echo(
            f"Session {choice} is not in this window's candidate set "
            f"({sorted(candidate_set)}). Try again."
        )


def _reset_session_choice_cache() -> None:
    """Test helper: clear the per-process session-choice cache."""
    global _session_choice_cache
    _session_choice_cache = None


def _check_task_ownership(item_id: int, current_eid: int | None) -> bool:
    """Resolve the live ownership state of `item_id` from `current_eid`'s view.

    Returns True if `current_eid` already owns the task (caller short-
    circuits with an "already active" notice). Returns False if the task
    is free (or only stale sessions hold it). Raises click.ClickException
    if a *different* live session owns the task.
    """
    rows = db.query(
        "SELECT id AS eid FROM sessions "
        "WHERE active_task_id = ? AND state != 'ended'",
        (item_id,),
    )
    if not rows:
        return False

    owned_by_current = current_eid is not None and any(
        r["eid"] == current_eid for r in rows
    )
    candidate_eids = [r["eid"] for r in rows if r["eid"] != current_eid]
    if not candidate_eids:
        return owned_by_current

    from endless.session_cmd import _live_sessions, _project_root_for_cwd
    project_root = _project_root_for_cwd()
    live = _live_sessions(project_root)
    live_by_eid = {
        c["endless_session_id"]: c
        for c in live
        if isinstance(c.get("endless_session_id"), int)
    }

    for eid in candidate_eids:
        comp = live_by_eid.get(eid)
        if comp is None:
            continue
        pane = comp.get("pane_id") or "?"
        raise click.ClickException(
            f"E-{item_id} is already active in session {eid} "
            f"(tmux pane {pane}).\n"
            "Switch to that session or have it release the task first."
        )

    return owned_by_current


_CLAIM_REQUIRES_FORCE: frozenset[str] = frozenset({
    "verify", "confirmed", "declined", "obsolete", "assumed", "completed",
})


# E-1555: statuses a task can be reopened from. `declined`/`obsolete` carry an
# explicit "we chose not to do this" decision — reversing them is an
# intentional act that should use `task update --status` and `--reason`,
# not a generic reopen. `verify` is not terminal: it's "implementation done,
# trust pending" — reopening it would discard pending verification rather
# than reactivate completed work.
_REOPENABLE_TERMINAL_STATUSES: frozenset[str] = frozenset({
    "assumed", "confirmed", "completed",
})


def _perform_claim_work(
    item_id: int,
    title: str | None,
    current_status: str,
    target_session: int | None,
    proj_name: str,
):
    """Emit claim events, print status/binding/worktree lines, create the worktree.

    Returns (wt_path, created). Caller has already validated the
    done-ish-status gate and the multi-owner refusal — this helper only
    does the mutation half of a claim.

    target_session=None is the spawn pre-claim case (Claude not yet
    started); skips the task.claimed event entirely. SessionStart's
    spawn-marker auto-bind records the binding once Claude is up.
    """
    from endless.event_bridge import emit_event
    from endless.worktree_cmd import create_task_worktree, _project_root

    # E-1401: pass the resolved session explicitly so emit_event doesn't
    # re-resolve via _current_endless_session_id (which would race the
    # binding we just established, or fail outright when called from a
    # plain shell during spawn pre-claim).
    session_id_arg = str(target_session) if target_session is not None else None

    # E-1500: secure the worktree FIRST. If creation refuses (orphan branch
    # carrying real work, a DB/file plan mismatch, an undeletable branch),
    # the task's status is left untouched rather than stranded in_progress.
    project_root = _project_root()
    slug_source = title or "task"
    wt_path, created = create_task_worktree(item_id, slug_source, project_root)

    if current_status != "in_progress":
        emit_event(
            kind="task.status_changed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={
                "old_status": current_status,
                "new_status": "in_progress",
            },
            session_id=session_id_arg,
        )
        _emit_field_changes(
            item_id, title,
            [("status", current_status, "in_progress")],
        )

    if target_session is not None:
        emit_event(
            kind="task.claimed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={"session_id": target_session},
            session_id=str(target_session),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" bound to session {target_session}"
        )

    click.echo("")

    home = str(Path.home())
    wt_display = (
        str(wt_path).replace(home, "~", 1)
        if str(wt_path).startswith(home)
        else str(wt_path)
    )
    state = "created" if created else "already exists"
    click.echo(
        click.style("•", fg="cyan")
        + f" worktree {state}: {wt_display}"
    )

    # Best-effort post-claim sweep of stale landed worktrees (E-1337).
    try:
        from endless.worktree_cmd import _reap_stale_worktrees
        _reap_stale_worktrees(project_root)
    except Exception:
        pass

    return wt_path, created


def claim_item(item_id: int, force: bool = False):
    """Claim ownership of a task and bind a Claude session to it.

    `force` covers two distinct override gates (single flag for one
    "I know what I'm doing" intent):
      - Bypasses the done-ish status gate (verify/confirmed/declined/
        obsolete/assumed/completed → in_progress demotion)
      - Allows claim WITHOUT a Claude session binding when no session
        can be resolved (manual-work-without-Claude case, E-1242)

    Resolves the binding target as: (1) current Endless session via
    ENDLESS_SESSION_ID / TMUX_PANE; (2) single sibling Claude session in
    the same tmux window (auto-pick); (3) on a tty, multi-sibling case
    displays `endless session list --project <project>` and prompts for
    a session ID. Off-tty multi-sibling refuses loudly. If no session
    resolves and not force: refuse.
    """
    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    current_status = row[0]["status"]
    if not force and current_status in _CLAIM_REQUIRES_FORCE:
        raise click.ClickException(
            f"E-{item_id} is in status '{current_status}'; re-claiming "
            f"would demote it to 'in_progress'.\n"
            "Pass --force to confirm the demotion, run "
            f"`endless task bind E-{item_id}` to attach this session "
            "to the task for status-bar display without changing its "
            "status, or update the status first if that's not what "
            "you intended."
        )

    _, proj_name = _resolve_project(None)
    target_session = _resolve_session_id_with_prompt(
        project_name=proj_name,
        prompt_verb="claimed for",
    )
    if target_session is None:
        if not force:
            raise click.ClickException(
                "No Claude session available to bind this task to "
                "(not running inside a Claude session, and no sibling "
                "Claude pane in this tmux window).\n"
                "Pass --force to claim without a session binding "
                "(manual work, no Claude assistance)."
            )
        # --force with no resolvable session: claim without a binding.

    if _check_task_ownership(item_id, target_session):
        from endless.worktree_cmd import create_task_worktree, _project_root
        click.echo(
            click.style("•", fg="cyan")
            + f" E-{item_id} is already active in session {target_session}"
        )
        try:
            project_root = _project_root()
        except click.ClickException:
            return
        slug_source = row[0]["title"] or "task"
        wt_path, _ = create_task_worktree(item_id, slug_source, project_root)
        home = str(Path.home())
        wt_display = (
            str(wt_path).replace(home, "~", 1)
            if str(wt_path).startswith(home)
            else str(wt_path)
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" worktree: {wt_display}"
        )
        # Best-effort post-claim sweep (E-1337).
        try:
            from endless.worktree_cmd import _reap_stale_worktrees
            _reap_stale_worktrees(project_root)
        except Exception:
            pass
        return

    _perform_claim_work(
        item_id=item_id,
        title=row[0]["title"],
        current_status=current_status,
        target_session=target_session,
        proj_name=proj_name,
    )

    click.echo("")
    click.echo("  To work on this task, choose one:")
    click.echo("    1. Delegate to a fresh Claude session:")
    click.echo(f"         endless task spawn E-{item_id}")
    click.echo("    2. Do it yourself in THIS Claude session:")
    wt = _worktree_for_task(item_id)
    if wt is not None:
        # /cd points Claude's own working directory at the worktree, so every
        # tool (Read/Write/Edit + a fresh Bash) defaults to it instead of main.
        # Absolute path: /cd does not expand ~ or $(...). Until you run this, a
        # claimed session is refused tool use from main (E-1586).
        click.echo(f"         /cd {wt}   # point Claude's working dir at the worktree (do this first)")
    eswt_cmd = f"eswt E-{item_id}"
    if _eswt_defined_in_user_shell():
        click.echo(f"         {eswt_cmd}   # (shell only) cd + ENDLESS_SESSION_ID routing")
    else:
        eval_cmd = 'eval "$(endless shell-init)"'
        pad = " " * (len(eval_cmd) - len(eswt_cmd))
        click.echo(f"         {eval_cmd}  # adds eswt shell helper func")
        click.echo(f"         {eswt_cmd}{pad}  # (shell only) cd + ENDLESS_SESSION_ID routing")


def bind_item(item_id: int) -> None:
    """Bind a Claude session to a task for status-bar display only.

    Symmetric counterpart to `release_item`: bind sets the session's
    active_task_id, release clears it. Unlike `claim_item`, bind does
    NOT change the task's status and does NOT create a worktree.

    Use when the task is already in `assumed` / `confirmed` / `verify`
    and the user wants the status row to keep showing it as context.
    `claim --force` is the wrong tool there because it demotes status
    back to `in_progress`.

    Target session resolution mirrors `claim_item`: env var / pane-
    direct / single-sibling auto-pick / on-a-tty multi-sibling prompt.
    Refuses when no session resolves — bind without a session is
    meaningless (nothing for the status bar to display).

    Emits a `task.claimed` event (the existing event added in E-1242);
    the Go executor performs the sessions DB write.
    """
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
    current_status = row[0]["status"]

    _, proj_name = _resolve_project(None)
    target_session = _resolve_session_id_with_prompt(
        project_name=proj_name,
        prompt_verb="bound to",
    )
    if target_session is None:
        raise click.ClickException(
            "No Claude session available to bind this task to "
            "(not running inside a Claude session, and no sibling "
            "Claude pane in this tmux window).\n"
            "Bind only makes sense when a session exists for the "
            "status bar to read from."
        )

    emit_event(
        kind="task.claimed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={"session_id": target_session},
        # E-1401: bind_item just resolved target_session above; pass it
        # explicitly so emit_event doesn't re-resolve.
        session_id=str(target_session),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" E-{item_id} bound to session {target_session} for display "
          f"(task status unchanged: {current_status})"
    )


def release_item(item_id: int | None, ignore_missing: bool = False) -> None:
    """Release a session's claim on a task.

    Two modes:
      - Bare `release` (item_id is None): release whatever task the current
        session is bound to. Requires resolving the current session id;
        errors with a pointer to the explicit-ID form if it can't.
      - `release E-NNN` (item_id given): clear the binding for whichever
        session owns E-NNN, regardless of who's asking. If a *different*
        live session owns it, refuse (preserves E-1203's exclusive-ownership
        invariant). If the binding is stale (DB row exists but no live
        companion), auto-clear and report. If no session has E-NNN bound:
        error UNLESS ignore_missing then info.

    Leaves tasks.status unchanged and leaves the worktree intact. Emits
    a `task.released` event whose Go executor clears the binding.
    """
    from endless.event_bridge import emit_event
    from endless.session_cmd import _live_sessions, _project_root_for_cwd

    current_eid = _current_endless_session_id()

    if item_id is None:
        if current_eid is None:
            raise click.ClickException(
                "Cannot resolve current session id "
                "(set ENDLESS_SESSION_ID or run inside a tmux pane with a "
                "known companion file).\n"
                "To release a specific task, pass its ID: "
                "endless task release E-NNN"
            )
        rows = db.query(
            "SELECT active_task_id FROM sessions WHERE id = ?",
            (current_eid,),
        )
        if not rows or rows[0]["active_task_id"] is None:
            click.echo("No task currently claimed by this session.")
            return
        target_id = rows[0]["active_task_id"]
        target_session = current_eid
    else:
        rows = db.query(
            "SELECT id FROM sessions "
            "WHERE active_task_id = ? AND state != 'ended'",
            (item_id,),
        )
        if not rows:
            msg = f"E-{item_id} is not currently claimed by any session."
            if ignore_missing:
                click.echo(msg)
                return
            raise click.ClickException(msg)

        owning_session = rows[0]["id"]
        if owning_session != current_eid:
            project_root = _project_root_for_cwd()
            live = _live_sessions(project_root)
            live_match = next(
                (
                    c for c in live
                    if c.get("endless_session_id") == owning_session
                ),
                None,
            )
            if live_match is not None:
                pane = live_match.get("pane_id") or "?"
                raise click.ClickException(
                    f"E-{item_id} is held by session {owning_session} "
                    f"(live; tmux pane {pane}).\n"
                    "Refusing to release another live session's claim."
                )
            click.echo(
                click.style("•", fg="cyan")
                + f" clearing stale binding for E-{item_id} "
                f"(session {owning_session} is no longer alive)"
            )

        target_id = item_id
        target_session = owning_session

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.released",
        project=proj_name,
        entity_type="task",
        entity_id=str(target_id),
        payload={"session_id": target_session},
        # E-1401: release_item resolved target_session above (either
        # current session releasing its own claim, or owner of E-NNN
        # when a specific id was passed); pass it explicitly so
        # emit_event doesn't re-resolve via the live resolver.
        session_id=str(target_session),
    )
    click.echo(
        click.style("•", fg="cyan")
        + f" released claim on E-{target_id} (session {target_session})"
    )


def _reopen_task_core(item_id: int) -> tuple[str, str, bool]:
    """Reopen a terminal-status task back to `ready` or `needs_plan`.

    Shared core for the `task reopen` verb and `task spawn --reopen` flag.
    Validates eligibility, releases any lingering session→task binding,
    and emits `task.status_changed`. Caller renders the result line.

    Returns (prev_status, new_status, text_present).
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status, text "
        "FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    current_status = row[0]["status"]

    if current_status in ("declined", "obsolete"):
        raise click.ClickException(
            f"E-{item_id} is '{current_status}'; reverse that decision "
            f"explicitly via `endless task update E-{item_id} --status "
            f"<status>` (and supply `--reason` if reopening a declined "
            f"task)."
        )

    if current_status not in _REOPENABLE_TERMINAL_STATUSES:
        raise click.ClickException(
            f"E-{item_id} is '{current_status}'; reopen is only valid from "
            f"a terminal status ({', '.join(sorted(_REOPENABLE_TERMINAL_STATUSES))})."
        )

    text_present = bool((row[0]["text"] or "").strip())
    new_status = "ready" if text_present else "needs_plan"

    _, proj_name = _resolve_project(None)

    # Clear any lingering session→task binding before flipping status.
    # Rare for terminal tasks (worktree land releases), but the plan calls
    # for it explicitly so retrospective queries see a clean handoff.
    bound_sessions = db.query(
        "SELECT id AS eid FROM sessions WHERE active_task_id = ?",
        (item_id,),
    )
    for s in bound_sessions:
        emit_event(
            kind="task.released",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={"session_id": s["eid"]},
            session_id=str(s["eid"]),
        )

    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": current_status,
            "new_status": new_status,
            "cascade": False,
        },
    )

    _emit_field_changes(
        item_id,
        row[0]["title"],
        [("status", current_status, new_status)],
        suffix=f"(text: {'present' if text_present else 'absent'})",
    )

    return current_status, new_status, text_present


def reopen_item(item_id: int) -> None:
    """Flip a terminal-status task back to actionable state.

    Standalone verb: no worktree side effects, no session binding. Caller
    decides next step (spawn, claim, or hand-back). For spawn-with-reopen
    in one shot, use `endless task spawn <id> --reopen`.
    """
    _reopen_task_core(item_id)


def update_plan(
    item_id: int,
    status: str | None = None,
    title: str | None = None,
    description: str | None = None,
    text_file: str | None = None,
    parent_id: int | None = None,
    phase: str | None = None,
    tier: int | None = None,
    task_type: str | None = None,
    analysis: str | None = None,
    outcome: str | None = None,
    force: bool = False,
    justification: str | None = None,
):
    """Update fields on a task."""
    from endless.event_bridge import emit_event

    _require_outcome_for_declined(status, outcome)

    row = db.query(
        "SELECT id, title, description, text, notes, status, "
        "       COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type, "
        "       phase, tier, parent_id, outcome, analysis "
        "FROM   tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    # E-1577: outcome-required check considers the merged value (incoming
    # --outcome overrides existing DB value; otherwise existing satisfies).
    # Lets the workflow "author outcome first → flip status later" work
    # without forcing a redundant --outcome re-pass.
    effective_outcome = outcome if (outcome and outcome.strip()) else row[0]["outcome"]
    _require_outcome_for_completed(status, effective_outcome)
    if title is not None:
        validate_title(title, force=force)

    if description is not None:
        validate_description(description)

    # Validate status if provided
    if status is not None:
        valid = ("needs_plan", "ready", "in_progress",
                 "verify", "confirmed", "assumed", "completed",
                 "blocked", "revisit", "declined", "obsolete")
        if status not in valid:
            raise click.ClickException(
                f"Invalid status '{status}'. "
                f"Valid: {', '.join(valid)}"
            )
        # E-1240: gate `completed` on a completable lead verb. Use the
        # incoming title if provided (the title is being changed in the
        # same call), else the existing title on the row.
        effective_title = title if title is not None else row[0]["title"]
        _require_completable_verb_for_completed(status, effective_title)
        # E-1577: research/epic tasks reject 'assumed'/'confirmed' terminals.
        # Use the incoming task_type if --type is also being set in this
        # update, else the existing type on the row.
        effective_type = task_type if task_type is not None else row[0]["type"]
        _require_terminal_allowed_for_type(status, effective_type)

    # Build the fields map for the event payload, plus an ordered list of
    # (name, old, new) tuples for change-output rendering.
    fields = {}
    changes: list = []

    def _add(name: str, new_value):
        fields[name] = new_value
        changes.append((name, row[0][name], new_value))

    if status is not None:
        _add("status", status)

    if phase is not None:
        _add("phase", phase)

    if title is not None:
        _add("title", title)

    if description is not None:
        _add("description", description)

    if text_file is not None:
        p = Path(text_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        text_content = p.read_text()
        _add("text", text_content)
        _mirror_plan_to_worktree(item_id, text_content)

    if parent_id is not None:
        _add("parent_id", parent_id if parent_id > 0 else None)

    if tier is not None:
        if tier == TIER_CLEAR:
            _add("tier", None)
        else:
            _add("tier", tier)
            # Tier 1 tasks can't be needs_plan; auto-advance to ready
            if tier == 1 and status is None and row[0]["status"] == "needs_plan":
                _add("status", "ready")

    if outcome is not None:
        _add("outcome", outcome)

    if task_type is not None:
        valid_types = ("task", "bug", "research", "epic")
        if task_type not in valid_types:
            raise click.ClickException(
                f"Invalid task type {task_type!r}. "
                f"Valid: {', '.join(valid_types)}"
            )
        # Map to the event payload key, which uses the column name
        # "type" (renamed in the row dict via the SELECT alias would
        # collide with Python's `type` builtin in the function-arg
        # signature; the wire field is "type").
        fields["type"] = task_type
        changes.append(("type", row[0]["type"], task_type))

    # E-1544: research-gate fires on update only when --type research is
    # being set in this update (regardless of the task's current type).
    # Effective parent = the new --parent if changing, else the existing
    # parent_id from the row. PARENT_NONE (0) means "make root" → None.
    if task_type == "research":
        if parent_id is not None:
            effective_parent = None if parent_id == PARENT_NONE else parent_id
        else:
            effective_parent = row[0]["parent_id"]
        _research_gate_check(effective_parent, justification)

    if justification:
        new_notes = _compose_justification_notes(row[0]["notes"], justification)
        if new_notes is not None:
            _add("notes", new_notes)

    if analysis is not None:
        _add("analysis", analysis)

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

    # Header title reflects the new title if it was changed in this update.
    header_title = fields.get("title", row[0]["title"]) or row[0]["description"]
    _emit_field_changes(item_id, header_title, changes)


def recover_task_text(item_id: int, text: str) -> None:
    """Set tasks.text for item_id, emitting task.fields_updated.

    Used by create_task_worktree (E-1500) to recover a plan from an orphan
    branch's committed plan file back into the DB — the source of truth —
    when tasks.text was empty. Kept separate from update_item so worktree_cmd
    can call it without dragging in the full update flow (and to avoid the
    worktree-mirroring step: the worktree is recreated fresh right after).
    """
    from endless.event_bridge import emit_event

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.fields_updated",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={"fields": {"text": text}},
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


def _task_landings(item_id: int) -> list:
    """All landing rows for a task, newest first (E-1478).

    Landing is append-only — `endless worktree land` writes one
    task_landings row per land — so a task can have more than one.
    """
    return db.query(
        "SELECT branch, merge_commit_sha, landed_at "
        "FROM task_landings WHERE task_id = ? "
        "ORDER BY landed_at DESC, id DESC",
        (item_id,),
    )


def _format_landed_line(landings: list) -> str:
    """Render the most recent landing as 'TS  shortsha  (landed N times)'.

    Caller guarantees `landings` is non-empty and newest-first. The
    '(landed N times)' suffix appears only when more than one landing exists.
    """
    latest = landings[0]
    sha = (latest["merge_commit_sha"] or "")[:7]
    parts = [_format_timestamp(latest["landed_at"])]
    if sha:
        parts.append(sha)
    if len(landings) > 1:
        parts.append(f"(landed {len(landings)} times)")
    return "  ".join(parts)


def detail_item(
    item_id: int,
    show_description: bool = True,
    show_analysis: bool = False,
    show_text: bool = False,
    show_children: bool = False,
    show_outcome: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Show full detail for a task."""
    row = db.query(
        "SELECT t.id, t.title, t.description, t.analysis, t.text, t.phase, t.status, "
        "COALESCE(tt.slug, '') AS type, "
        "t.parent_id, t.source_file, t.created_at, t.updated_at, "
        "t.completed_at, t.sort_order, t.tier, t.outcome, p.name as project_name "
        "FROM tasks t "
        "JOIN projects p ON t.project_id = p.id "
        "LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE t.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    item = row[0]
    landings = _task_landings(item_id)

    if as_json:
        import json
        out = {
            "id": f"E-{item['id']}",
            "title": item["title"],
            "project": item["project_name"],
            "type": item["type"],
            "phase": item["phase"],
            "status": item["status"],
            "parent": f"E-{item['parent_id']}" if item["parent_id"] else None,
            "created": item["created_at"],
            "updated": item["updated_at"],
            "confirmed": item["completed_at"] or None,
            "landed": (
                {
                    "landed_at": landings[0]["landed_at"],
                    "merge_commit_sha": landings[0]["merge_commit_sha"],
                    "branch": landings[0]["branch"],
                    "count": len(landings),
                }
                if landings else None
            ),
            "source_file": item["source_file"] or None,
            "tier": item["tier"],
            "outcome": item["outcome"] or None,
            "description": item["description"] if show_description else None,
            "analysis": item["analysis"] if show_analysis else None,
            "text": item["text"] if show_text else None,
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
        click.echo(f"project={item['project_name']}")
        tier_str = f" tier={tier_display(item['tier'])}" if item["tier"] else ""
        click.echo(f"type={item['type']} phase={item['phase']} "
                    f"status={item['status']}{tier_str}")
        if item["outcome"]:
            click.echo(f"outcome={item['outcome']}")
        if item["parent_id"]:
            click.echo(f"parent=E-{item['parent_id']}")
        links = _flatten_relations(item_id)
        if links:
            link_str = ",".join(f"E-{r['id']} ({r['rel']})" for r in links)
            click.echo(f"links={link_str}")
        click.echo(f"created={item['created_at']}")
        click.echo(f"updated={item['updated_at']}")
        if item["completed_at"]:
            click.echo(f"confirmed={item['completed_at']}")
        if landings:
            click.echo(f"landed={_format_landed_line(landings)}")
        if show_description and item["description"] and item["description"] != item["title"]:
            click.echo(f"\n## Description\n{item['description']}")
        if show_analysis and item["analysis"]:
            click.echo(f"\n## Analysis\n{item['analysis']}")
        if show_text and item["text"]:
            click.echo(f"\n## Text\n{item['text']}")
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
    col_w = 11  # width of label column (longest: "Confirmed:" = 10 + 1 space)
    label = lambda s: click.style(f"{s:<{col_w}}", fg="cyan")
    val = lambda s: click.style(str(s), fg="white", bold=True)

    click.echo()
    click.echo(click.style("Task Detail", fg="green", bold=True))
    click.echo(click.style("───────────", dim=True))

    click.echo(f"{label('ID:')} {val(task_id_display(item['id']))}")
    click.echo(f"{label('Title:')} {val(item['title'])}")
    click.echo(f"{label('Project:')} {val(item['project_name'])}")
    click.echo(f"{label('Type:')} {val(item['type'])}")
    click.echo(f"{label('Phase:')} {val(item['phase'])}")
    click.echo(f"{label('Status:')} {val(item['status'])}")
    if item["tier"]:
        click.echo(f"{label('Tier:')} {val(tier_display(item['tier']))}")
    if item["parent_id"]:
        click.echo(f"{label('Parent:')} {val(task_id_display(item['parent_id']))}")
    click.echo(f"{label('Created:')} {val(_format_timestamp(item['created_at']))}")
    if item["updated_at"] and item["updated_at"] != item["created_at"]:
        click.echo(f"{label('Updated:')} {val(_format_timestamp(item['updated_at']))}")
    if item["completed_at"]:
        click.echo(f"{label('Confirmed:')} {val(_format_timestamp(item['completed_at']))}")
    if landings:
        click.echo(f"{label('Landed:')} {val(_format_landed_line(landings))}")
    if item["source_file"]:
        click.echo(f"{label('Source:')} {val(item['source_file'])}")
    # Links last: multi-line block sits below the single-line fields (E-1477).
    _echo_links_section(item_id)

    # Large text sections
    if show_description and item["description"] and item["description"] != item["title"]:
        click.echo()
        click.echo(click.style("— Description —", fg="cyan"))
        click.echo(item["description"])

    # Analysis precedes Text: it is pre-plan design content (E-999).
    if show_analysis and item["analysis"]:
        click.echo()
        click.echo(click.style("— Analysis —", fg="cyan"))
        click.echo(item["analysis"])

    if show_text and item["text"]:
        click.echo()
        click.echo(click.style("— Text —", fg="cyan"))
        click.echo(item["text"])

    if item["outcome"] and (show_outcome or item["status"] in ("declined", "completed")):
        click.echo()
        click.echo(click.style("— Outcome —", fg="cyan"))
        click.echo(item["outcome"])

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


# The spawn handoff is generated, not stored (E-1469). Per-task variables
# (id, title) plus runtime context (the spawning pane, the spawning session's
# task) are merged into the embedded `handoff` template at spawn time. The
# rendering lives in `endless-go template render` (E-1565); this module
# shells out per render.


def _branch_for_worktree(wt_path) -> str | None:
    """Current git branch of a worktree, or None if it can't be read."""
    import subprocess
    try:
        out = subprocess.run(
            ["git", "-C", str(wt_path), "branch", "--show-current"],
            capture_output=True, text=True, check=True,
        )
        return out.stdout.strip() or None
    except Exception:
        return None


_HANDOFF_TYPES = frozenset({"task", "bug", "research", "epic"})


def render_handoff(spawned_id: int, title: str,
                   return_anchor: str | None,
                   spawner_task_id: int | None,
                   worktree_path: str | None = None,
                   branch: str | None = None,
                   task_type: str | None = None,
                   bg: bool = False) -> str:
    """Render the spawn handoff for a task by invoking `endless-go template render`.

    The handoff is mostly boilerplate (orient, read the guide + plan, default
    interaction rules, return path, closing); only the task id, title, the
    spawning pane, and the task's worktree/branch vary. Generating it means
    agents no longer author prompts, so prompt-vs-plan drift cannot occur.
    See E-1469. E-1565 moved the rendering surface from Python's
    string.Template to Go's text/template — Python builds the var map and
    shells out per render. E-1566 split the single template into
    per-type variants under `handoff/{task,bug,research,epic}.md.tmpl`;
    unknown or null `task_type` falls back to the task variant. The child
    count is universal — per E-1552, every variant includes a conditional
    line naming the count when nonzero.

    `bg=True` (E-1568) renders the background-agent variant of each template:
    a headless `claude --bg` agent has no spawning pane to return to and no
    tmux window to move, so the `{{if .bg}}` branch in each template drops the
    `tmux switch-client`/`tmux move-window` return lines and instead tells the
    agent to do the work, flip the task to `verify`, and stop (the user attaches
    later via `claude attach <short_id>`).
    """
    import json
    import subprocess
    from endless.event_bridge import _resolve_endless_go

    effective_type = task_type if task_type in _HANDOFF_TYPES else "task"
    child_rows = db.query(
        "SELECT count(*) AS n FROM tasks WHERE parent_id = ?",
        (spawned_id,),
    )
    child_count = child_rows[0]["n"] if child_rows else 0

    vars_payload = {
        "spawned_id": spawned_id,
        "title": title,
        "spawner_task": spawner_task_id if spawner_task_id is not None else "?",
        "return_anchor": return_anchor or "%<spawning-pane>",
        "worktree_path": worktree_path or "<task worktree>",
        "branch": branch or "<task branch>",
        "child_count": child_count,
        "bg": bg,
    }
    binary = _resolve_endless_go()
    result = subprocess.run(
        [binary, "template", "render", f"handoff/{effective_type}"],
        input=json.dumps(vars_payload),
        capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        raise click.ClickException(
            f"endless-go template render failed: {result.stderr.strip()}"
        )
    return result.stdout


def show_handoff(item_id: int):
    """Render the spawn handoff for a task and print it."""
    row = db.query(
        "SELECT t.id, t.title, COALESCE(tt.slug, '') AS type_slug "
        "FROM tasks t LEFT JOIN task_types tt ON tt.id = t.type_id "
        "WHERE t.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )
    wt = _worktree_for_task(item_id)
    click.echo(render_handoff(
        item_id,
        row[0]["title"],
        os.environ.get("TMUX_PANE"),
        _current_session_active_task_id(),
        worktree_path=str(wt) if wt else None,
        branch=_branch_for_worktree(wt) if wt else None,
        task_type=row[0]["type_slug"] or None,
    ))


_SPAWN_WINDOW_STOP_WORDS = frozenset({
    "a", "an", "the", "to", "from", "of", "for", "with",
    "in", "on", "at", "by", "and", "or",
})


def _spawn_window_name(project_name: str, title: str, item_id: int) -> str:
    """Build tmux window name in the form <project>_<one_or_two_words>[E-nnn].

    Separator is '_' because tmux parses ':' as session:window and '.' as
    window.pane in -t targets, so either char in a window name breaks
    'select-window -t <name>' / 'send-keys -t <name>' even within one session.
    """
    words = re.findall(r"[a-z0-9]+", title.lower())
    meaningful = [w for w in words if w not in _SPAWN_WINDOW_STOP_WORDS]
    slug_words = meaningful or words or ["task"]
    slug = "-".join(slug_words[:2])
    return f"{project_name}_{slug}[{task_id_display(item_id)}]"


def _claude_binary() -> str:
    """Resolve the `claude` binary path, avoiding shell function wrappers.

    Prefers `~/.local/bin/claude` if present (the canonical install location),
    else falls back to `claude` on PATH. Shared by the foreground spawn flow,
    the `--bg` dispatch flow, and the attach verbs (E-1570).
    """
    claude_bin = os.path.expanduser("~/.local/bin/claude")
    if not os.path.exists(claude_bin):
        claude_bin = "claude"
    return claude_bin


def _lookup_bg_short_id(task_id: int) -> str | None:
    """Return the short id of the live background agent for a task, or None.

    Single source of truth for both attach verbs (E-1570). Matches the row
    written by `--bg` dispatch: a `working` session of kind `background`
    bound to this task. The `session_kinds` subselect keeps the lookup
    resolving even if the seed row id ever changes. ORDER BY id DESC LIMIT 1
    returns the most recent dispatch if more than one exists.
    """
    rows = db.query(
        "SELECT short_id FROM sessions "
        "WHERE active_task_id = ? "
        "AND kind_id = (SELECT id FROM session_kinds WHERE slug = 'background') "
        "AND state = 'working' "
        "ORDER BY id DESC LIMIT 1",
        (task_id,),
    )
    return rows[0]["short_id"] if rows else None


def spawn_plan(item_id: int, project_name: str | None = None, no_plan: bool = False,
               worktree: str | None = None, force: bool = False,
               reopen: bool = False, bg: bool = False, attach: bool = False):
    """Spawn a new tmux window with Claude working on a task's prompt.

    Pre-claims the task (status flip + worktree creation) BEFORE launching
    Claude, so the spawned session lands in a worktree on a task that is
    already in_progress. The SessionStart hook reads
    `@endless_spawned_by` from the new tmux window and records the
    session→task binding via `BindSessionToTask` (no redundant status
    flip). See E-1274.

    `reopen=True` (E-1555) reopens an `assumed`/`confirmed`/`completed`
    target as a pre-step (status → `ready`/`needs_plan` based on text
    presence) before proceeding with spawn. Errors on non-terminal or
    decision-bearing (`declined`/`obsolete`) statuses.

    `bg=True` (E-1568) dispatches the agent headless via `claude --bg --name
    E-<id>` instead of a tmux window. No tmux is required; the same done-ish
    gate, pre-claim, and worktree creation run first. The dispatch row is
    written with session_id NULL + the short id parsed from `claude --bg`
    stdout; the agent's SessionStart hook fills in the real UUID later. `--bg`
    ignores `--no-plan` (headless agents have no `/plan` slash concept).

    `attach=True` (E-1570) is a view modifier, not a dispatcher: it opens a NEW
    tmux window running `claude attach <short-id>` against the task's already
    live background agent. It requires a `--bg` row to exist (does NOT dispatch)
    and is mutually exclusive with `--bg`. Detaching the attached window leaves
    the background agent running.
    """
    import shutil
    import subprocess
    import tempfile

    if attach and bg:
        raise click.ClickException(
            "--attach and --bg are mutually exclusive: --bg dispatches a new "
            "background agent, --attach opens a window onto an existing one. "
            "To do both, run `endless task spawn --bg` then "
            "`endless task spawn --attach`."
        )

    if reopen and force:
        raise click.ClickException(
            "--reopen and --force are mutually exclusive: --reopen sets "
            "status to ready/needs_plan (handoff intent), --force demotes "
            "to in_progress (self-pickup intent). Pick one."
        )

    # tmux is the delivery surface for the foreground path only; a `--bg`
    # agent is headless, so the tmux requirement is bypassed for it.
    if not bg:
        if not shutil.which("tmux"):
            raise click.ClickException("tmux is not installed")
        if not os.environ.get("TMUX"):
            raise click.ClickException(
                "Not in a tmux session. "
                "endless spawn requires tmux."
            )

    # Get the plan item
    row = db.query(
        "SELECT p.id, p.title, p.status, p.project_id, "
        "proj.path as project_path, proj.name as project_name, "
        "COALESCE(tt.slug, '') AS type_slug "
        "FROM tasks p "
        "JOIN projects proj ON p.project_id = proj.id "
        "LEFT JOIN task_types tt ON tt.id = p.type_id "
        "WHERE p.id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )
    item = row[0]

    title = item["title"]
    current_status = item["status"]

    # E-1570: --attach is a view modifier. It opens a NEW tmux window onto the
    # task's already-live background agent (via `claude attach`); it does NOT
    # pre-claim, dispatch, or render a handoff. Branch here, after the task
    # lookup (needed for the window name) and the tmux gate above.
    if attach:
        short_id = _lookup_bg_short_id(item_id)
        if not short_id:
            raise click.ClickException(
                f"{task_id_display(item_id)} has no live bg agent. Dispatch "
                f"with `endless task spawn --bg {task_id_display(item_id)}` "
                f"first."
            )
        window_name = _spawn_window_name(
            item["project_name"], title, item_id,
        )
        subprocess.run(
            ["tmux", "new-window", "-n", window_name],
            check=True,
        )
        # Diagnostic only; not load-bearing for the attach itself.
        subprocess.run(
            ["tmux", "set", "-w", "-t", window_name,
             "@endless_attached_short_id", short_id],
            check=True,
        )
        subprocess.run(
            ["tmux", "send-keys", "-t", window_name,
             f"{_claude_binary()} attach {short_id}", "Enter"],
            check=True,
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Attached window '{window_name}' to bg agent "
            + click.style(f"{task_id_display(item_id)}: {title}", bold=True)
            + f" ({short_id})"
        )
        click.echo(f"  Switch to it: tmux select-window -t {window_name}")
        click.echo("  Detach (leaves the agent running): ← or Ctrl+Z")
        return

    # --worktree overrides the cd target so the spawned session reads
    # .claude/settings.json from the worktree (worktree-local hook override
    # via 'just claude-settings-init' applies). tmux send-keys would not
    # surface a bad cd, so validate up front.
    if worktree is not None:
        cd_target = os.path.abspath(os.path.expanduser(worktree))
        if not os.path.isdir(cd_target):
            raise click.ClickException(
                f"--worktree path does not exist or is not a directory: "
                f"{cd_target}"
            )
    else:
        cd_target = None  # default below to the spawn-created worktree

    # E-1555: --reopen explicit intent gate.
    if reopen:
        if current_status not in _REOPENABLE_TERMINAL_STATUSES:
            if current_status in ("declined", "obsolete"):
                raise click.ClickException(
                    f"E-{item_id} is '{current_status}'; reverse that "
                    f"decision explicitly via `endless task update "
                    f"E-{item_id} --status <status>` (and supply "
                    f"`--reason` if reopening a declined task)."
                )
            raise click.ClickException(
                f"--reopen passed but E-{item_id} is '{current_status}', "
                f"not terminal (reopen targets "
                f"{', '.join(sorted(_REOPENABLE_TERMINAL_STATUSES))})."
            )
        # Reopen pre-step: flip terminal → ready/needs_plan, release any
        # lingering session binding, emit audit event.
        _reopen_task_core(item_id)
        # _perform_claim_work below sees the post-reopen status and
        # promotes ready/needs_plan → in_progress on its own.
        current_status = db.query(
            "SELECT status FROM tasks WHERE id = ?", (item_id,),
        )[0]["status"]

    # Mirror claim's done-ish-status gate
    elif not force and current_status in _CLAIM_REQUIRES_FORCE:
        if current_status in _REOPENABLE_TERMINAL_STATUSES:
            raise click.ClickException(
                f"E-{item_id} is '{current_status}'; pass --reopen to "
                f"reopen-and-spawn, or run `endless task reopen "
                f"E-{item_id}` first."
            )
        raise click.ClickException(
            f"E-{item_id} is in status '{current_status}'; spawning "
            f"would demote it to 'in_progress'.\n"
            "Pass --force to confirm the demotion, or update the status "
            "first if that's not what you intended."
        )

    # Refuse if another live session already owns the task. Passing
    # current_eid=None treats any owner as "other" — spawn never claims
    # ownership for the spawning session.
    _check_task_ownership(item_id, current_eid=None)

    # Pre-claim: emit status_changed, create worktree. No session binding
    # yet — Claude hasn't started. SessionStart's @endless_spawned_by
    # path will record the binding once the new session is up.
    _, proj_name = _resolve_project(None)
    wt_path, _ = _perform_claim_work(
        item_id=item_id,
        title=title,
        current_status=current_status,
        target_session=None,
        proj_name=proj_name,
    )

    if cd_target is None:
        cd_target = str(wt_path)

    # E-1568: background dispatch. Diverges from the tmux flow entirely — no
    # window, no send-keys, no plan-mode paste. Render the bg handoff variant,
    # launch `claude --bg --name E-<id>` with the handoff as positional argv,
    # parse the short id from stdout, and record the dispatch row.
    if bg:
        _spawn_bg_dispatch(
            item_id=item_id,
            title=title,
            cd_target=cd_target,
            task_type=item["type_slug"] or None,
            worktree_override=worktree is not None,
        )
        return

    # Spawner identity for the @endless_spawned_by marker. Prefer the
    # current Endless session id; fall back to a pid-prefixed value so
    # non-Claude spawners (CLI from a plain shell) still set a non-empty
    # marker that SessionStart can key off.
    spawner_id = _current_endless_session_id() or f"pid-{os.getpid()}"

    # Build window name: <project>_<one_or_two_words>[E-nnn]
    window_name = _spawn_window_name(
        item["project_name"], title, item_id,
    )

    # Render the handoff from the template (no stored prompt — E-1469) and
    # write it to a temp file for tmux load-buffer. The spawning pane is the
    # return anchor; the spawning session's active task is the origin line;
    # cd_target is the worktree the spawned session lands in.
    handoff_text = render_handoff(
        item_id, title, os.environ.get("TMUX_PANE"),
        _current_session_active_task_id(),
        worktree_path=cd_target,
        branch=_branch_for_worktree(cd_target),
        task_type=item["type_slug"] or None,
    )
    handoff_file = tempfile.NamedTemporaryFile(
        mode="w", suffix=".md", prefix="endless-handoff-",
        delete=False,
    )
    handoff_file.write(handoff_text)
    handoff_file.close()

    # Create tmux window and set plan metadata
    subprocess.run(
        ["tmux", "new-window", "-n", window_name],
        check=True,
    )
    subprocess.run(
        ["tmux", "set", "-w", "-t", window_name,
         "@endless_spawned_by", str(spawner_id)],
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

    # cd to target directory (spawn-created worktree, or --worktree path)
    subprocess.run(
        ["tmux", "send-keys", "-t", window_name,
         f"cd {cd_target}", "Enter"],
        check=True,
    )

    # Launch claude (use binary directly to avoid shell function wrappers)
    claude_bin = _claude_binary()
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
        ["tmux", "load-buffer", handoff_file.name],
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
    os.unlink(handoff_file.name)

    click.echo(
        click.style("•", fg="cyan")
        + f" Spawned window '{window_name}' for "
        + click.style(f"{task_id_display(item_id)}: {title}", bold=True)
    )
    if worktree is not None:
        click.echo(f"  cwd: {cd_target}")
    click.echo(
        f"  Switch to it: tmux select-window -t {window_name}"
    )


# First stdout line of `claude --bg`:  "backgrounded · <short-id> · <name>"
# (`·` is U+00B7; no ANSI codes per docs/research-2026-06-12-claude-background-
# agents.md §2). The short id is the dispatch handle used by `claude attach`.
_BG_SHORT_ID_RE = re.compile(r"^backgrounded\s+·\s+([0-9a-f]+)\s+·\s+", re.M)


def _parse_bg_short_id(stdout: str) -> str | None:
    """Extract the dispatch short id from `claude --bg` stdout, or None."""
    m = _BG_SHORT_ID_RE.search(stdout)
    return m.group(1) if m else None


def _spawn_bg_dispatch(item_id: int, title: str, cd_target: str,
                       task_type: str | None, worktree_override: bool):
    """Dispatch a background agent for an already-pre-claimed task (E-1568).

    Renders the bg handoff variant, launches `claude --bg --name E-<id>` with
    the handoff as a positional argv (well under ARG_MAX), parses the short id
    from stdout, and records the dispatch sessions row (session_id NULL +
    short_id, kind background) via the `session-query record-bg-agent` Go
    helper. The agent's SessionStart hook attaches the real UUID later.
    """
    import subprocess
    from endless import config
    from endless.event_bridge import _resolve_endless_go

    # No spawning pane / origin return for a headless agent — the bg template
    # branch omits the tmux return lines, so return_anchor is unused.
    handoff_text = render_handoff(
        item_id, title, None,
        _current_session_active_task_id(),
        worktree_path=cd_target,
        branch=_branch_for_worktree(cd_target),
        task_type=task_type,
        bg=True,
    )

    claude_bin = _claude_binary()

    try:
        result = subprocess.run(
            [claude_bin, "--bg", "--name", f"E-{item_id}", handoff_text],
            cwd=cd_target, capture_output=True, text=True,
        )
    except FileNotFoundError as e:
        raise click.ClickException(f"claude not found: {e}")
    if result.returncode != 0:
        raise click.ClickException(
            f"claude --bg failed (exit {result.returncode}):\n"
            f"{result.stderr.strip() or result.stdout.strip()}"
        )

    short_id = _parse_bg_short_id(result.stdout)
    if not short_id:
        # Never proceed with a missing handle — the dispatch row would be
        # un-attachable and un-decoratable.
        raise click.ClickException(
            "could not parse the dispatch short id from `claude --bg` stdout:\n"
            f"{result.stdout.strip()}"
        )

    # Write the dispatch row Go-side (resolves project_id + epic ancestor;
    # no Python DB read, per E-1486).
    binary = _resolve_endless_go()
    rec = subprocess.run(
        [binary, *config.go_db_context_args(),
         "session-query", "record-bg-agent",
         "--task-id", str(item_id), "--short-id", short_id],
        capture_output=True, text=True,
    )
    if rec.returncode != 0:
        raise click.ClickException(
            f"recording bg-agent session failed: {rec.stderr.strip()}"
        )

    click.echo(
        click.style("•", fg="cyan")
        + " Backgrounded "
        + click.style(f"{task_id_display(item_id)}: {title}", bold=True)
        + f" as {short_id}"
    )
    if worktree_override:
        click.echo(f"  cwd: {cd_target}")
    click.echo(f"  Attach: claude attach {short_id}")


def task_attach_impl(item_id: int, force: bool = False):
    """Replace the current process with `claude attach` for a task's bg agent.

    The `attach` verb (E-1570) is meant to be run from a fresh shell: it execs
    `claude attach <short-id>` in place, so the calling process is GONE on
    success (no return). Detaching the attached view leaves the bg agent
    running.

    Refuses (unless --force) when run inside a Claude session
    (`CLAUDECODE == "1"`), because the exec would replace — and thus kill — the
    caller's own Claude/coordinator process.

    Go-port note: this becomes `exec.LookPath("claude")` +
    `syscall.Exec(path, ["claude", "attach", short_id], os.Environ())`; POSIX
    execve semantics are identical, only PATH lookup becomes explicit.
    """
    short_id = _lookup_bg_short_id(item_id)
    if not short_id:
        raise click.ClickException(
            f"{task_id_display(item_id)} has no live bg agent."
        )

    if os.environ.get("CLAUDECODE") == "1" and not force:
        click.echo(
            click.style(
                "You are inside a Claude session. `endless task attach` "
                "replaces the current process; you will lose this session.\n"
                "Re-run with --force to proceed, or open a fresh terminal.",
                fg="red",
            ),
            err=True,
        )
        raise SystemExit(1)

    # Replaces this process; nothing after this line runs on success.
    os.execvp("claude", ["claude", "attach", short_id])


def search_tasks(
    query: str,
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: list[str] | None = None,
    phase_filter: str | None = None,
    parent_id: int | None = None,
    search_text: bool = False,
    limit: int = 20,
    llm: bool = False,
    as_json: bool = False,
):
    """Search tasks by query string across ID, title, and description."""
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE t.project_id = ?"
    params: list = [project_id]

    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND t.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += " AND t.status NOT IN ('confirmed', 'assumed', 'completed', 'declined', 'obsolete')"
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
            f"{task_id_display(target_id)} ({', '.join(names)}). Specify --type <type>."
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


def replace_task(old_id: int, new_id: int, status: str = "obsolete", outcome: str | None = None):
    """Mark old_id as replaced by new_id. Sets old to `status` (default 'obsolete') and records relationship."""
    from endless.event_bridge import emit_event

    _require_outcome_for_declined(status, outcome)

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

    old_status_row = db.query(
        "SELECT COALESCE(title, description) as title, status "
        "FROM   tasks WHERE id = ?", (old_id,)
    )
    payload = {
        "old_status": old_status_row[0]["status"],
        "new_status": status,
        "cascade": False,
    }
    if outcome:
        payload["outcome"] = outcome
    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(old_id),
        payload=payload,
    )

    changes = [("status", old_status_row[0]["status"], status)]
    if outcome:
        changes.append(("outcome", None, outcome))
    _emit_field_changes(
        old_id,
        old_status_row[0]["title"],
        changes,
        suffix=f"(replaced by {task_id_display(new_id)})",
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


# Statuses that count as "done" for relation-row coloring (E-1477).
_RELATION_TERMINAL_STATUSES = ("confirmed", "assumed", "completed", "declined", "obsolete")


def _flatten_relations(item_id: int) -> list[dict]:
    """Flatten get_all_relations into one id-ascending list, each entry carrying the
    directional, lower-cased relation label, for the unified 'Links:' rendering (E-1477)."""
    flat: list[dict] = []
    for display_name, items in get_all_relations(item_id).items():
        rel = RELATION_LABELS.get(display_name, display_name).lower()
        for d in items:
            flat.append({"id": d["id"], "rel": rel, "status": d["status"]})
    flat.sort(key=lambda r: r["id"])
    return flat


def _echo_links_section(item_id: int) -> bool:
    """Emit the unified multi-line 'Links:' section (E-1477): a cyan 'Links:' heading,
    then one indented, colored row per relation (id-ascending) — 'E-NNN (relation
    type) [status]'. Titles are intentionally omitted to keep every row on one line.
    Emits nothing and returns False when the task has no relations; returns True
    otherwise. Shared by task show/detail and relations/deps so both render identically."""
    links = _flatten_relations(item_id)
    if not links:
        return False
    click.echo(click.style("Links:", fg="cyan"))
    for r in links:
        color = "green" if r["status"] in _RELATION_TERMINAL_STATUSES else "yellow"
        click.echo(
            f"  {task_id_display(r['id'])} ({r['rel']}) "
            f"[{click.style(r['status'], fg=color)}]")
    return True


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


def show_relations(item_id: int, llm: bool = False):
    """Show all of a task's relations under a single 'Links:' section (E-1477)."""
    if not db.exists("SELECT 1 FROM tasks WHERE id = ?", (item_id,)):
        raise click.ClickException(f"Task {task_id_display(item_id)} not found.")

    if llm:
        click.echo(f"# Relations for E-{item_id}")
        links = _flatten_relations(item_id)
        if not links:
            click.echo("(none)")
            return
        link_str = ", ".join(f"E-{r['id']} ({r['rel']})" for r in links)
        click.echo(f"Links: {link_str}")
        return

    click.echo()
    click.echo(click.style(f"Relations for {task_id_display(item_id)}", fg="green", bold=True))
    click.echo(click.style("─" * 30, dim=True))
    if not _echo_links_section(item_id):
        click.echo("  (none)")
    click.echo()
