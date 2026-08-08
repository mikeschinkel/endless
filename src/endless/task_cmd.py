"""Task command logic — import, show, and manage task items."""

import contextlib
import io
import os
import re
import shutil
import subprocess
import sys
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
    """Ask a model whether `word` is a verb (E-1264).

    Invokes the `claude` binary directly via PATH lookup — no shell, no alias
    expansion, so users' `claude` shell wrappers are bypassed.

    The model is resolved through `config.internal_model("verb_check")`
    (E-1859), the one resolver every internal call shares, rather than being
    hardcoded here. Its default is still `haiku`, so behavior is unchanged
    until a user configures otherwise.

    Returns (True, definition) only on a clean `YES: <text>` reply with a
    non-empty definition. Every other outcome — `NO`, malformed output,
    timeout, missing binary, non-zero exit — returns (False, None), causing
    the caller to fall through to the standard verb-rejection error.
    """
    import subprocess
    from endless import config, internal_claude
    prompt = _VERB_CHECK_PROMPT_TEMPLATE.format(word=word)
    try:
        # Hook-suppressed (E-1470): a bare `claude -p` here inherits the
        # caller's TMUX_PANE and false-ends the live caller's session.
        result = internal_claude.run_internal_claude(
            prompt, model=config.internal_model("verb_check"), timeout=30,
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


def session_id_display(session_id: int) -> str:
    """Format an endless session ID for display: ES-123 (E-1261).

    Sessions and tasks are separate id spaces that both used to render as
    `E-NNN`, so a session id sitting next to a task id in the same block was
    indistinguishable. The two-letter `ES-` prefix disambiguates. Introduced
    here for `task show`'s Created:/Touched by: (E-1866); sweeping the rest of
    the session surfaces is E-1261's job.
    """
    return f"ES-{session_id}"


def _hierarchical_label_prefix(item_id: int, parent_id: int | None) -> str:
    """Hierarchical id prefix for bg-agent labels (E-1620).

    A task with a parent renders `E-<parent>/E-<id>`; a root task (standalone
    or epic) renders the bare `E-<id>`. The rule keys solely on parent
    presence, regardless of the parent's type.
    """
    if parent_id:
        return f"{task_id_display(parent_id)}/{task_id_display(item_id)}"
    return task_id_display(item_id)


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


def _mirror_doc_to_worktree(
    task_id: int, subdir: str, label: str, content: str,
) -> Path | None:
    """Mirror one multiline document field into the task's worktree IF one exists.

    Generalizes the E-1445 plan mirror to every version-controlled doc field
    (E-1747): plan/outcome/analysis each land in their own
    `<worktree>/.endless/<subdir>/E-NNN.md`. The DB column is the source of
    truth and is written separately via the task event payload; this mirrors
    that content to a committed file as the durability belt.

    It NEVER creates a worktree (rescinds the E-1216 auto-create default,
    which surprised callers by provisioning worktrees + sandboxes for tasks
    they had no intention of working on yet). When no worktree exists, the DB
    is updated and nothing is written to disk; the mirror materializes later
    when the worktree is born at claim/spawn
    (`worktree_cmd.create_task_worktree` → `_materialize_task_docs`).

    Returns the written path, or None when no worktree exists.
    """
    wt_path = _worktree_for_task(task_id)
    if wt_path is None:
        return None

    docs_dir = wt_path / ".endless" / subdir
    docs_dir.mkdir(parents=True, exist_ok=True)
    target = docs_dir / f"E-{task_id}.md"
    target.write_text(content)
    click.echo(
        click.style("✓", fg="green")
        + f" Wrote {label} to {_display_path(target)}"
    )
    from endless.worktree_cmd import _commit_doc_in_worktree
    _commit_doc_in_worktree(
        wt_path, f".endless/{subdir}/E-{task_id}.md",
        f"Endless: update {label} for E-{task_id}",
    )
    return target


def _mirror_plan_to_worktree(task_id: int, content: str) -> Path | None:
    """Back-compat alias: mirror the plan (text) field. Prefer
    `_mirror_doc_to_worktree` for arbitrary doc fields (E-1747)."""
    return _mirror_doc_to_worktree(task_id, "plans", "plan", content)


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
                    "status": "unplanned",
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
    llm: bool = False,
    as_json: bool = False,
    type_filter: str | None = None,
):
    """Show tasks for a project as a flat sorted table.

    When type_filter is set (e.g. "epic"), the query joins task_types and
    keeps only rows whose type slug matches — this is the single-source
    listing path shared by `endless epic list`.
    """
    project_id, proj_name = _resolve_project(project_name)

    join = ""
    where = "WHERE pi.project_id = ?"
    params: list = [project_id]
    if type_filter is not None:
        join = " JOIN task_types tt ON tt.id = pi.type_id"
        where += " AND tt.slug = ?"
        params.append(type_filter)
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
    if not sort_by:
        sort_by = "id"
    order_by = sort_col_map.get(sort_by, "pi.sort_order")

    rows = db.query(
        f"SELECT pi.id, pi.phase, COALESCE(pi.title, pi.description) as title, "
        f"pi.description, pi.status, pi.parent_id, "
        f"pi.created_at, pi.completed_at, pi.tier "
        f"FROM tasks pi{join} {where} "
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

    _render_flat_table(rows)

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
    # E-1845: `untriaged` is excluded — a task nobody has looked at yet is not
    # actionable work, and offering it here would present it as a ready-to-pick-
    # up item. It surfaces in `session status` (as ◌ triage) instead, which is
    # where the routing decision belongs.
    where = (
        "WHERE t.status NOT IN ('confirmed', 'assumed', 'completed', 'blocked', 'declined', 'obsolete', 'underway', 'unverified', 'submitted', 'untriaged') "
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
        f"    WHEN 'ready' THEN 0 WHEN 'unplanned' THEN 1 "
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
    """Show tasks that are underway or awaiting verification."""
    where = "WHERE t.status IN ('underway', 'unverified')"
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
        f"    WHEN 'underway' THEN 0 WHEN 'unverified' THEN 1 END, "
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


# --- task unsettled (E-1865) -----------------------------------------------
#
# The inverse of `task landed`: `task landed` answers "what has reached main?",
# `task unsettled` answers "what hasn't, and why?".
#
# ED-1540 vocabulary: a worktree is SETTLED when its working tree is clean AND
# its branch is fully in main. It is UNSETTLED when it is MODIFIED (uncommitted
# changes) or UNLANDED (commits not in main). `session status` collapses both
# into one ◆; these commands expand it, because the two need opposite fixes —
# commit-or-discard vs land.
#
# The verdict is NOT computed here. It comes from the same Go probe that drives
# the ◆ (monitor.WorktreeUnsettledAt via `session-query worktree-unsettled`), so
# the marker and its explanation cannot drift apart. Python resolves tasks to
# worktree paths and renders; Go decides.


def _unsettled_probe(paths: list[Path]) -> list[dict]:
    """Return the Go unsettled breakdown for each path, in the same order.

    One subprocess for the whole batch: the list view inspects every active
    worktree, and a per-worktree invocation would make it visibly slow.

    Path-based (never --task-id) for E-1766's reason: inside a self-dev worktree
    a DB lookup routes to the per-worktree sandbox, which has no task row.
    """
    if not paths:
        return []
    binary = shutil.which("endless-go")
    if not binary:
        raise click.ClickException("endless-go not found on PATH")
    result = subprocess.run(
        [binary, "session-query", "worktree-unsettled", *(str(p) for p in paths)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise click.ClickException(
            f"worktree-unsettled probe failed: {result.stderr.strip() or result.returncode}"
        )
    import json
    try:
        return json.loads(result.stdout)
    except ValueError as exc:
        raise click.ClickException(f"unreadable probe output: {exc}") from exc


def _worktree_path_for_task(root: Path, item_id: int) -> Path | None:
    """Return the task's canonical worktree dir if it exists on disk, else None.

    E-971/ED-1515 convention: `<root>/.endless/worktrees/e-<id>`, bare form only.
    """
    path = root / ".endless" / "worktrees" / f"e-{item_id}"
    return path if path.is_dir() else None


def _unsettled_rows(project_id: int, root: Path) -> list[dict]:
    """Probe every task worktree in the project; return rows joined with task data.

    Enumerates worktrees from DISK rather than from the tasks table so a worktree
    whose task row is missing or stale still gets reported — the point of the
    command is to explain state the user can see, not state the DB believes.
    """
    from endless.worktree_cmd import _enriched_list, _task_id_from_worktree_path

    candidates: list[tuple[int, Path]] = []
    for wt in _enriched_list(root):
        if wt["state"] != "active":
            continue
        display = _task_id_from_worktree_path(Path(wt["path"]))
        if display is None:
            continue
        candidates.append((int(display.removeprefix("E-")), Path(wt["path"])))

    if not candidates:
        return []

    probes = _unsettled_probe([p for _, p in candidates])
    ids = [i for i, _ in candidates]
    # db.query yields sqlite3.Row, which has no .get() — materialize plain dicts
    # so a task id with no row falls back cleanly below.
    titles = {
        r["id"]: {"title": r["title"], "status": r["status"], "phase": r["phase"]}
        for r in db.query(
            "SELECT t.id, COALESCE(t.title, t.description) AS title, t.status, t.phase "
            "FROM tasks t WHERE t.project_id = ? AND t.id IN "
            f"({','.join('?' * len(ids))})",
            (project_id, *ids),
        )
    }

    rows = []
    for (item_id, path), probe in zip(candidates, probes):
        meta = titles.get(item_id) or {}
        rows.append({
            "id": item_id,
            "title": meta.get("title") or "(no task row)",
            "status": meta.get("status") or "?",
            "phase": meta.get("phase") or "?",
            "path": str(path),
            "probe": probe,
        })
    return rows


def unsettled_list(
    project_name: str | None = None,
    limit: int = 20,
    include_settled: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Survey every task worktree in the project, with the reason for each (E-1865).

    Reached via `task unsettled --all`; the CLI requires an explicit target
    because this walks every worktree on disk and probes each with git.

    `include_settled` additionally lists the settled worktrees, so the command
    can answer "is anything outstanding?" with a complete picture, not silence.
    """
    project_id, proj_name = _resolve_project(project_name)
    from endless.worktree_cmd import _project_root
    root = _project_root()

    rows = _unsettled_rows(project_id, root)
    if not include_settled:
        rows = [r for r in rows if r["probe"]["unsettled"]]
    # Modified-and-unlanded first, then modified, then unlanded: the rows needing
    # the most work sort to the top, and ties fall back to task id.
    rows.sort(key=lambda r: (
        -(r["probe"]["modified"] + r["probe"]["unlanded"]), r["id"]))
    shown = rows[:limit]

    if as_json:
        import json
        click.echo(json.dumps([
            {
                "id": task_id_display(r["id"]),
                "title": r["title"],
                "status": r["status"],
                "project": proj_name,
                "worktree": r["path"],
                **r["probe"],
            }
            for r in shown
        ], indent=2))
        return

    if not shown:
        if llm:
            click.echo("# no unsettled worktrees")
        else:
            click.echo(click.style("•", fg="cyan") +
                       " No unsettled worktrees — everything is committed and landed")
        return

    if llm:
        click.echo(f"# {proj_name}")
        for r in shown:
            click.echo(f"{task_id_display(r['id'])} {r['probe']['reason']} "
                       f"{r['status']} {r['title']}")
        _echo_unsettled_truncation(len(rows), len(shown), llm=True)
        return

    click.echo()
    click.echo(click.style(f"Unsettled ({proj_name}):", bold=True))
    id_w = max(len(task_id_display(r["id"])) for r in shown)
    reason_w = max(len(r["probe"]["reason"]) for r in shown)
    for r in shown:
        title = r["title"]
        if len(title) > 44:
            title = title[:43] + "…"
        # Pad BEFORE styling: click.style wraps the text in ANSI escapes, which
        # would otherwise be counted by the width spec and eat the padding.
        reason = f"{r['probe']['reason']:<{reason_w}}"
        click.echo(
            f"  {task_id_display(r['id']):<{id_w}}  "
            f"{click.style(reason, fg=_unsettled_color(r['probe']))}  "
            f"{title}"
        )
    _echo_unsettled_truncation(len(rows), len(shown), llm=False)
    click.echo()
    click.echo(click.style("  ", fg="cyan") +
               f"Detail for one: endless task unsettled <id>")
    click.echo()


def _unsettled_color(probe: dict) -> str:
    """Colour a reason by which fix it demands: yellow to commit, cyan to land."""
    if not probe["unsettled"]:
        return "green"
    return "yellow" if probe["modified"] else "cyan"


_PROBE_ERROR_LABELS = (("status_error", "git status"), ("rev_list_error", "git rev-list"))


def _probe_errors(probe: dict) -> list[tuple[str, str]]:
    """Return [(label, message)] for each git probe that failed."""
    return [(label, probe[key]) for key, label in _PROBE_ERROR_LABELS if probe.get(key)]


def _echo_probe_errors(probe: dict) -> None:
    """Surface failed git probes.

    Both the predicate and the ◆ marker are fail-open — any git error is read as
    settled — so a silent failure would present as "nothing to do". Saying the
    verdict may under-report is the only honest rendering.
    """
    for label, msg in _probe_errors(probe):
        click.echo()
        click.echo(click.style(
            f"  Note: {label} failed ({msg}); the ◆ marker treats a git error "
            f"as settled, so this verdict may under-report.", fg="red"))


def _echo_unsettled_truncation(total: int, shown: int, llm: bool) -> None:
    """Say what --limit hid, so a truncated list is never mistaken for the whole."""
    if total <= shown:
        return
    hidden = total - shown
    if llm:
        click.echo(f"# {hidden} more (raise --limit)")
    else:
        click.echo(f"  … {hidden} more (raise --limit to see {total})")


def unsettled_item(item_id: int, llm: bool = False, as_json: bool = False):
    """Explain exactly why one task's worktree is unsettled (E-1865)."""
    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) AS title, t.status, "
        "p.name AS project_name "
        "FROM tasks t JOIN projects p ON t.project_id = p.id WHERE t.id = ?",
        (item_id,),
    )
    from endless.worktree_cmd import _project_root
    root = _project_root()
    path = _worktree_path_for_task(root, item_id)

    if row:
        item = {"id": row[0]["id"], "title": row[0]["title"],
                "status": row[0]["status"], "project_name": row[0]["project_name"]}
    elif path is not None:
        # A worktree on disk with no task row is exactly the confusing state this
        # command exists to explain, so describe the worktree rather than
        # refusing. Only a missing task AND no worktree is a genuine bad id.
        item = {"id": item_id, "title": "(no task row)",
                "status": "?", "project_name": "?"}
    else:
        raise click.ClickException(f"No task found with id {item_id}")
    probe = _unsettled_probe([path])[0] if path else {
        "has_worktree": False, "unsettled": False, "modified": False,
        "unlanded": False, "reason": "no worktree", "branch": "",
        "modified_files": [], "auto_managed_files": [],
        "unlanded_count": 0, "unlanded_log": [],
    }

    if as_json:
        import json
        click.echo(json.dumps({
            "id": task_id_display(item["id"]),
            "title": item["title"],
            "status": item["status"],
            "project": item["project_name"],
            **probe,
        }, indent=2))
        return

    if llm:
        click.echo(f"# {task_id_display(item['id'])} {item['title']}")
        click.echo(f"# {probe['reason']}")
        for f in probe["modified_files"]:
            click.echo(f"modified {f}")
        for f in probe["auto_managed_files"]:
            click.echo(f"auto-managed {f}")
        for c in probe["unlanded_log"]:
            click.echo(f"unlanded {c}")
        return

    click.echo()
    click.echo(click.style(
        f"{task_id_display(item['id'])} ({item['title']})", bold=True))
    if probe["branch"]:
        click.echo(f"  Branch:    {probe['branch']}")
    if path:
        click.echo(f"  Worktree:  {path}")
    click.echo(f"  Verdict:   " +
               click.style(probe["reason"], fg=_unsettled_color(probe)))

    if not probe["unsettled"]:
        click.echo()
        if not probe["has_worktree"]:
            click.echo(click.style("•", fg="cyan") +
                       " No worktree for this task — nothing to land.")
        elif _probe_errors(probe):
            # Fail-open: a failed probe reads as settled. Claiming the tree is
            # clean here would assert something git never actually told us.
            click.echo(click.style("•", fg="red") +
                       " Cannot confirm settled — a git probe failed (see below).")
        else:
            click.echo(click.style("•", fg="green") +
                       " Settled: working tree clean and every commit is on main.")
        _echo_probe_errors(probe)
        click.echo()
        return

    if probe["modified_files"]:
        click.echo()
        click.echo(click.style(
            f"  Modified — {len(probe['modified_files'])} uncommitted/untracked "
            f"file(s). Fix: commit or discard.", bold=True))
        for f in probe["modified_files"]:
            click.echo(f"    {f}")

    if probe["auto_managed_files"]:
        click.echo()
        click.echo(click.style(
            f"  Auto-managed — {len(probe['auto_managed_files'])} endless-owned "
            f"file(s). `worktree land` commits these for you.", bold=True))
        for f in probe["auto_managed_files"]:
            click.echo(f"    {f}")

    if probe["unlanded"]:
        click.echo()
        click.echo(click.style(
            f"  Unlanded — {probe['unlanded_count']} commit(s) not on main. "
            f"Fix: endless worktree land {task_id_display(item['id'])}", bold=True))
        for c in probe["unlanded_log"]:
            click.echo(f"    {c}")
        if len(probe["unlanded_log"]) < probe["unlanded_count"]:
            click.echo(f"    … {probe['unlanded_count'] - len(probe['unlanded_log'])} more")

    _echo_probe_errors(probe)
    click.echo()


# E-1544: research-gate helpers. ED-1504 requires `--type research` to be
# justified unless `--parent` is a type=epic, status=underway task.

_RESEARCH_GATE_MSG = (
    "--type research requires --justification explaining why the "
    "research can't be inline in a do-task."
)

_JUSTIFICATION_HEADING_RE = re.compile(r"(?m)^##\s+Justification\b")


def _research_gate_check(parent_id: int | None, justification: str | None) -> None:
    """Raise click.ClickException if the research gate fails.

    Pass when (a) `justification` is non-empty, or (b) parent is a
    type=epic, status=underway task. Sticky-override statuses
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
    if row[0]["type_slug"] != "epic" or row[0]["status"] != "underway":
        raise click.ClickException(_RESEARCH_GATE_MSG)


def _reject_maybe_with_parent(phase: str | None, parent_id: int | None) -> None:
    """Reject a task that is both maybe-phase and parented.

    maybe = uncommitted; parent-child = scope binding. A parent with
    maybe-phase children has phantom scope — it appears done while
    uncommitted commitments linger inside. Hard rejection, no override.

    Callers pass the *effective* phase and parent_id (the values that would
    result from the write), so the same check covers create, field-update,
    and move.
    """
    if phase == "maybe" and parent_id is not None:
        raise click.ClickException(
            "A maybe-phase task cannot have a parent. "
            "maybe = uncommitted; parent-child = scope binding, and mixing the "
            "two creates phantom scope. Promote it (--phase now/next/later) to "
            "place it under a parent, or link it with a relates_to relation "
            "instead."
        )


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
    text: str | None = None,
    analysis: str | None = None,
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

    task_type = task_type or "todo"
    validate_title(title, force=force)
    validate_description(description)
    _reject_maybe_with_parent(phase, parent_id)
    _, proj_name = _resolve_project(project_name)
    # E-1845: a new task is `untriaged` — filed, not yet looked at. Triage
    # decides whether the description is already a sufficient spec (→ submitted)
    # or design work is needed first (→ unplanned). Tier-1's auto-`ready` is
    # unchanged: a tier-1 task is explicitly exempt from planning, so it is
    # exempt from triage too.
    status = status or ("ready" if tier == 1 else "untriaged")

    # E-1577/E-1579: research/epic tasks cannot be created in
    # 'unverified'/'assumed'/'confirmed'.
    _require_status_allowed_for_type(status, task_type)

    # E-1544: research-gate. Justification (when present) accepted-and-stored
    # even if parent is epic+underway (gate only governs *requiring* it).
    if task_type == "research":
        _research_gate_check(parent_id, justification)
    notes_value = _compose_justification_notes(None, justification)

    text_content: str | None = text

    payload = {
        "title": title,
        "description": description or "",
        "phase": phase,
        "status": status,
        "type": task_type,
    }
    if text_content is not None:
        payload["text"] = text_content
    if analysis is not None:
        payload["analysis"] = analysis
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
    if analysis is not None and analysis.strip():
        _mirror_doc_to_worktree(item_id, "analyses", "analysis", analysis)

    # E-1859: triage at file time, detached. A synchronous model call here
    # would add seconds to EVERY filing, interactive ones included, so this is
    # a latency optimization only — the background sweep is the correctness
    # guarantee. If the child never starts or dies, the task simply stays
    # `untriaged` and the sweep picks it up.
    #
    # Gated on the RESOLVED status, not on the absence of --status: that leaves
    # tier-1's auto-`ready` untouched (a tier-1 task is exempt from planning,
    # so it is exempt from triage), and equally skips any explicit --status.
    if status == "untriaged":
        from endless import triage
        triage.spawn_detached(item_id)

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
        status = item.get("status", "unplanned")
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


def _removal_id_set(item_id: int, cascade: bool) -> list[int]:
    """Every task id a `task remove` would delete.

    Without `--cascade` that is the one task; with it, the task plus its full
    descendant set. The guard below has to check all of them: if only the root
    were checked, removing a parent would delete a child *and* orphan the
    child's relations, bypassing the guard entirely.
    """
    if not cascade:
        return [item_id]
    rows = db.query(
        "WITH RECURSIVE tree(id) AS ("
        "  SELECT id FROM tasks WHERE id = ?"
        "  UNION ALL"
        "  SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id"
        ") SELECT id FROM tree",
        (item_id,),
    )
    return [r["id"] for r in rows]


def _decision_id_display(item_id: int) -> str:
    """ED-42. Local so this module need not import decision_cmd, which imports
    from here."""
    return f"ED-{item_id}"


def _endpoint_display(kind: str, item_id: int) -> str:
    return _decision_id_display(item_id) if kind == "decision" else task_id_display(item_id)


def _relations_referencing(ids: list[int]) -> list[tuple[int, str]]:
    """Relation rows holding any of `ids` as a TASK endpoint (E-1915).

    Returns (owning_task_id, clearing_command) pairs, ordered by row id within
    each table. `owning_task_id` is which of `ids` the row hangs off, so a
    `--cascade` refusal can name the descendant that is actually holding things
    up.

    Two tables, one exposure. `task_deps` carries task→task and task→decision
    rows discriminated by source_type/target_type; `decision_relations` carries
    the decision→task rows. Neither can declare a foreign key on the task
    endpoint — SQLite cannot express an FK whose target table varies by row —
    so a task delete leaves both behind, and task ids are reused.
    """
    if not ids:
        return []
    marks = ",".join("?" * len(ids))
    idset = set(ids)
    out: list[tuple[int, str]] = []

    for row in db.query(
        "SELECT source_type, source_id, target_type, target_id, dep_type "
        "FROM task_deps "
        f"WHERE (source_type = 'task' AND source_id IN ({marks})) "
        f"   OR (target_type = 'task' AND target_id IN ({marks})) "
        "ORDER BY id",
        tuple(ids) * 2,
    ):
        src = _endpoint_display(row["source_type"], row["source_id"])
        tgt = _endpoint_display(row["target_type"], row["target_id"])
        verb = "decision unlink" if row["source_type"] == "decision" else "task unlink"
        # Attribute to the source when both endpoints are being removed — the
        # command that clears the row is written from the source's side.
        owner = (
            row["source_id"]
            if row["source_type"] == "task" and row["source_id"] in idset
            else row["target_id"]
        )
        out.append((
            owner,
            f"endless {verb} {src} --to {tgt} --type {row['dep_type']}",
        ))

    for row in db.query(
        "SELECT source_decision_id, target_id, relation_type "
        "FROM decision_relations "
        f"WHERE target_kind = 'task' AND target_id IN ({marks}) "
        "ORDER BY id",
        tuple(ids),
    ):
        out.append((
            row["target_id"],
            f"endless decision unlink {_decision_id_display(row['source_decision_id'])} "
            f"--to {task_id_display(row['target_id'])} --type {row['relation_type']}",
        ))

    return out


def _refuse_removal_with_relations(item_id: int, cascade: bool) -> None:
    """Refuse to remove a task while any relation still references it (E-1915).

    Deny rather than cascade: a severed relation cannot be reconstructed, and a
    refusal costs one `unlink`. All relation types, no per-type exemption — one
    rule that always holds beats two rules with a judgment call at the boundary,
    and every "harmless" exemption is a second code path that can orphan rows
    again.

    Must run BEFORE the task.deleted event is emitted, or a refusal would
    publish a deletion that never happened.
    """
    ids = _removal_id_set(item_id, cascade)
    found = _relations_referencing(ids)
    if not found:
        return

    subject = task_id_display(item_id)
    lines = [
        f"{subject} has {len(found)} relation(s)."
        if not cascade else
        f"{subject} and its descendants have {len(found)} relation(s).",
        "Removing would orphan them — relation rows survive a task delete, and",
        "task ids are reused, so a later task inheriting one of these ids would",
        "inherit its relations too. Unlink them first:",
        "",
    ]
    if cascade:
        # Attribute each row to the id it hangs off, or the operator cannot
        # tell which of the tasks being deleted is holding up the removal.
        for owner in sorted({owner for owner, _ in found}):
            lines.append(f"  {task_id_display(owner)}:")
            lines += [f"      {cmd}" for o, cmd in found if o == owner]
    else:
        lines += [f"    {cmd}" for _, cmd in found]
    raise click.ClickException("\n".join(lines))


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

    # E-1915: refuse while relations still point at any id being deleted. There
    # is deliberately no flag to remove a task and its relations in one step —
    # `--cascade` above is about CHILDREN and predates this.
    _refuse_removal_with_relations(item_id, cascade)

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


def _reject_status_with_keep_status(status: str | None, keep_status: bool):
    """Refuse `--status X --keep-status` — the two flags ask for opposite things.

    E-1913: `--keep-status` means "no auto-transition fires; the status you see
    is the status you keep". Naming a status in the same call is a request to
    change it. Resolving that silently (either way) would teach the caller a
    precedence rule instead of telling them the call was contradictory.
    """
    if status is not None and keep_status:
        raise click.ClickException(
            "--status and --keep-status contradict each other: one sets the "
            "status, the other holds it. Pass --status alone to change it "
            "(an explicit status already suppresses every auto-transition), "
            "or --keep-status alone to leave it untouched."
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
    task_type: str | None = None,
):
    """E-1240: `completed` is gated to tasks whose title's lead verb is
    marked `completable: true` in verbs.jsonl. Reserves the status for
    findings-as-deliverable work (audits, research, reviews, …) and keeps
    implementation tasks on the `unverified`/`confirmed`/`assumed` track.

    ED-1511: epics are exempt. An epic's deliverable IS its outcome text (a
    coordination summary of what shipped in its children), and the type gate
    already forces epics to terminate via `completed` — so applying the verb
    gate to an implementation-verb-titled epic only deadlocks it."""
    if status != "completed":
        return
    if task_type == "epic":
        return
    if task_type == "brainstorm":
        # E-1657/ED-1516: like epics (ED-1511), the brainstorm *type* already
        # signals an information deliverable (the synthesis), so requiring a
        # separately `completable`-marked title verb on top is redundant — and
        # the auto-registered "Brainstorm" verb does not carry the flag.
        return
    from endless.matchers import is_completable_verb
    verb = _lead_verb(title)
    if not is_completable_verb(verb):
        raise click.ClickException(
            "'completed' isn't a valid final status for this task. "
            "Implementation tasks finish as 'confirmed' or 'assumed'."
        )


# Types whose deliverable IS the outcome text, so completing one requires
# --outcome. ED-1520: the requirement is keyed on task TYPE, not the 'completed'
# status (E-1240 had coupled it to status as a proxy for these types). Epics are
# excluded — they self-complete via child-status derivation, with no interactive
# completion step where an outcome could be supplied. Other types reaching
# 'completed' via a completable verb are not forced to carry an outcome.
_OUTCOME_REQUIRED_TYPES = ("research", "brainstorm")


def _require_outcome_for_completed(
    status: str | None,
    task_type: str | None,
    outcome: str | None,
):
    """ED-1520: completing a research/brainstorm task requires --outcome — the
    outcome IS the deliverable for those types. Keyed on type, not the
    'completed' status. Decline's own reason requirement is separate
    (`_require_outcome_for_declined`, ED-1022)."""
    if (status == "completed"
            and (task_type or "") in _OUTCOME_REQUIRED_TYPES
            and not (outcome and outcome.strip())):
        raise click.ClickException(
            f"An outcome is required when completing a {task_type} task — "
            "the outcome IS the deliverable. Use --outcome (or --outcome-file) "
            "to provide it."
        )


# Per-type lookup table of statuses a task of that type may not be set to.
# E-1577 seeded this for the 'assumed'/'confirmed' terminals; E-1579 adds
# 'unverified' — research and epic never go through user-testable verification
# (epics auto-derive to 'completed' per E-1541; research ends in 'completed
# --outcome' per ED-1502). Their only type-specific terminal is 'completed';
# the universal terminals 'obsolete' and 'declined' remain allowed for all
# types and are deliberately absent here. This Python table is the single
# type→status policy gate (ED-1506 keeps the Go executor mechanical); E-1543's
# epic-only super-gate will later extend the 'epic' entry.
_TYPE_FORBIDDEN_STATUSES = {
    "research":   ("unverified", "assumed", "confirmed"),
    "epic":       ("unverified", "assumed", "confirmed"),
    # E-1657/ED-1516: brainstorm's deliverable is the synthesis (information,
    # not testable behavior), so like research it terminates via 'completed
    # --outcome' and never goes through user-testable verification.
    "brainstorm": ("unverified", "assumed", "confirmed"),
}


def _require_status_allowed_for_type(status: str | None, task_type: str | None):
    """E-1577/E-1579: reject type-inappropriate statuses up front. research and
    epic tasks reject 'unverified'/'assumed'/'confirmed' — they terminate via
    'completed' (per E-1537 §3) and never go through verification. This is a
    type-correctness invariant, not a soft policy: the fix for a rejected flip
    is to change the task type, not to override the gate (so there is no
    --force bypass, matching E-1577's hard gate)."""
    forbidden = _TYPE_FORBIDDEN_STATUSES.get(task_type or "")
    if forbidden and status in forbidden:
        raise click.ClickException(
            f"Task type {task_type!r} cannot be set to status {status!r}. "
            f"{task_type} tasks terminate via 'completed' (with --outcome) and "
            f"never use 'unverified'/'assumed'/'confirmed'. Use --status completed, "
            f"or change the task type."
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


# E-1772: the agent-driven wind-down transitions on which we nudge the agent to
# stop composing freeform handoffs and route all further reporting through the
# `endless task report <id>` steering-prompt command (E-1771). Deliberately
# narrow: fires on underway->unverified, ->assumed, and ->completed (with an
# outcome); NOT on submitted/ready/confirmed, the claim's ->underway, or the
# revisit/declined/obsolete management transitions.
def _is_report_wind_down(old_status: str, new_status: str, outcome_present: bool) -> bool:
    """True when a status change is an agent's terminal wind-down (E-1772)."""
    if old_status == new_status:
        return False
    if new_status == "unverified":
        return old_status == "underway"
    if new_status == "assumed":
        return True
    if new_status == "completed":
        return outcome_present
    return False


def _maybe_emit_report_reminder(
    item_id: int, old_status: str, new_status: str, outcome_present: bool
) -> None:
    """Print the route-reporting-through-`task report` reminder on wind-down (E-1772).

    A no-op on every non-wind-down transition, so callers can invoke it
    unconditionally after any status change.

    The reminder steers an *agent*; a human running `task assume`/`complete`
    interactively should not see it. So it fires only when the invoker is an
    agent (CLAUDECODE=1) or a human explicitly asked to preview the agent's
    view with the global `--agent-view` flag — the same gate the agent `--help`
    augmentation uses."""
    from endless.agent_help import agent_view_requested

    if not (_running_under_agent() or agent_view_requested()):
        return
    if not _is_report_wind_down(old_status, new_status, outcome_present):
        return
    bullet = click.style("▸", fg="yellow")
    click.echo("")
    click.echo(
        bullet
        + " Report through the command, not prose. Route this handoff — and all"
    )
    click.echo(
        "  further status reporting for the rest of this session — through:"
    )
    click.echo(
        "      "
        + click.style(f"endless task report {task_id_display(item_id)}", bold=True)
    )
    click.echo(
        "  Answer in your own words, then append its block unchanged after the"
    )
    click.echo(
        "  separator. Add --json only for genuinely out-of-band notes or open"
    )
    click.echo("  questions.")


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

    _require_status_allowed_for_type("confirmed", row[0]["type"])
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

    if outcome and outcome.strip():
        _mirror_doc_to_worktree(item_id, "outcomes", "outcome", outcome)

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

    _require_status_allowed_for_type("assumed", row[0]["type"])
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

    if outcome and outcome.strip():
        _mirror_doc_to_worktree(item_id, "outcomes", "outcome", outcome)

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
    _maybe_emit_report_reminder(
        item_id, row[0]["status"], "assumed", bool(outcome and outcome.strip())
    )


def mark_completed_item(item_id: int, outcome: str):
    """E-1240: Mark a findings-as-deliverable task as `completed`.

    Gated by `--outcome` (required) and by `completable: true` on the
    task title's lead verb in verbs.jsonl. Distinct from `confirmed`
    (behavior verified) and `assumed` (behavior believed correct,
    awaiting promotion). Use for Audit/Research/Investigate/Review-style
    tasks whose deliverable is the outcome text itself."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status, "
        "       COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type "
        "FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    _require_outcome_for_completed("completed", row[0]["type"], outcome)
    _require_completable_verb_for_completed(
        "completed", row[0]["title"], row[0]["type"]
    )

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

    if outcome and outcome.strip():
        _mirror_doc_to_worktree(item_id, "outcomes", "outcome", outcome)

    changes = [
        ("status", row[0]["status"], "completed"),
        ("outcome", None, outcome),
    ]
    _emit_field_changes(item_id, row[0]["title"], changes)
    _maybe_emit_report_reminder(
        item_id, row[0]["status"], "completed", bool(outcome and outcome.strip())
    )


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

    if reason and reason.strip():
        _mirror_doc_to_worktree(item_id, "outcomes", "outcome", reason)

    changes = [
        ("status", row[0]["status"], "declined"),
        ("outcome", None, reason),
    ]
    _emit_field_changes(item_id, row[0]["title"], changes)


def _session_is_background(session_id: int | None) -> bool:
    """True when the given session is of kind `background`.

    Used to gate the human approval verb and non-`ready` pickup: a
    background loop must not approve its own work or claim work that a
    human has not approved. Returns False when session_id is None (no
    resolvable session → not provably background).
    """
    if session_id is None:
        return False
    rows = db.query(
        "SELECT 1 FROM sessions "
        "WHERE id = ? "
        "AND kind_id = (SELECT id FROM session_kinds WHERE slug = 'background')",
        (session_id,),
    )
    return bool(rows)


def _current_session_is_background() -> bool:
    """True when the session running this command is of kind `background`."""
    return _session_is_background(_current_endless_session_id())


# Statuses a task may be `submit`ted from: pre-approval design states.
#
# E-1845: `untriaged` is included so the new default status is not a dead end.
# E-1859 made the routing automatic, and `task submit` stays exactly as
# important: it is the permanent human override for a triage call you disagree
# with, and the route that still works when the triager is unreachable (it
# fails open, leaving the task here). Never scaffolding to remove.
_SUBMITTABLE_FROM = ("untriaged", "unplanned", "revisit")


def submit_item(item_id: int):
    """Mark a task as `submitted` — spec-complete, awaiting human approval.

    Agent-set. Reachable two ways, both landing here: the agent attached a
    plan (`tasks.text` populated — the plan-attach auto-move handles that in
    the executor) OR the agent judges the description a sufficient spec (no
    plan text, this verb). Plan-vs-no-plan is carried by `tasks.text`, not by
    status. A human then runs `endless task approve` to reach `ready`.
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

    current = row[0]["status"]
    if current == "submitted":
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already submitted"
        )
        return
    if current not in _SUBMITTABLE_FROM:
        raise click.ClickException(
            f"Cannot submit a task in status '{current}'; submit applies to "
            f"{' or '.join(_SUBMITTABLE_FROM)} tasks (spec-complete, awaiting "
            "approval)."
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": current,
            "new_status": "submitted",
            "cascade": False,
        },
    )

    _emit_field_changes(
        item_id, row[0]["title"], [("status", current, "submitted")]
    )


def approve_item(item_id: int):
    """Approve a `submitted` task → `ready` (the human approval gate).

    `ready` now provably means human-approved, so background loops may pick
    up only `ready` work. A `kind=background` session is refused here: it
    cannot approve its own work. (A tmux-driven agent running approve stays a
    convention — the system can't distinguish human from agent in a pane.)
    """
    from endless.event_bridge import emit_event

    if _current_session_is_background():
        raise click.ClickException(
            "A background session cannot approve a task. Approval is the "
            "human gate that promotes a task to 'ready'; run it from an "
            "interactive session."
        )

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    current = row[0]["status"]
    if current == "ready":
        click.echo(
            click.style("•", fg="cyan")
            + f" Item {task_id_display(item_id)} is already ready"
        )
        return
    if current != "submitted":
        raise click.ClickException(
            f"Cannot approve a task in status '{current}'; approve applies to "
            "'submitted' tasks (spec-complete, awaiting approval). Have the "
            "agent submit it first."
        )

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(item_id),
        payload={
            "old_status": current,
            "new_status": "ready",
            "cascade": False,
        },
    )

    _emit_field_changes(
        item_id, row[0]["title"], [("status", current, "ready")]
    )


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
    from endless.event_bridge import _resolve_endless_go
    from endless.session_cmd import _project_root_for_cwd

    try:
        project_root = _project_root_for_cwd()
    except Exception:
        return None
    pane = process if process is not None else os.environ.get("TMUX_PANE", "")

    # Resolve the binary the same way every other DB-opening shellout does
    # (E-1510), rather than taking whatever `endless-go` PATH happens to offer.
    # Under --db sandbox the global binary's embedded enums are a DIFFERENT
    # baseline from the sandbox DB's, and the fail-closed integrity check turns
    # that mismatch into a hard error — which this function then swallows as
    # "no session", silently disabling everything downstream of session
    # resolution. E-1901 hit exactly that: the relay checkpoint could never arm
    # in a self-dev worktree because the stale global refused the sandbox DB.
    try:
        go_bin = _resolve_endless_go()
    except Exception:
        return None

    args = [
        go_bin, *config.go_db_context_args(),
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
    # This is a disambiguation prompt over a candidate set the resolver already
    # settled, so the listing must show EVERY candidate — a filtered-out row the
    # user is still allowed to type reads as a bug in the prompt. Hence both
    # widenings (E-1914): all_projects when unnamed, so candidates outside cwd's
    # project are not dropped (and so the current-project default cannot raise
    # when cwd is not in a registered project), and show_all, so a candidate that
    # is hidden, empty, or has not claimed a task still appears.
    list_sessions(project_name=project_name, all_projects=not project_name,
                  show_all=True)
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
    # E-1807: self-heal a ghost owner first. A session that died without firing
    # SessionEnd leaves a non-ended row pointing at a now-dead tmux pane, which
    # the query below would read as a live owner. Reaping it (flip to `ended`,
    # NULL `process`) before the read lets a dead-pane owner fall out naturally,
    # so the spawn/claim proceeds with no user step. Best-effort: a reaper
    # failure is silent and falls through to the pre-E-1807 behavior.
    from endless.session_cmd import (
        _live_sessions, _project_root_for_cwd, _reap_dead_panes,
    )
    project_root = _project_root_for_cwd()
    _reap_dead_panes(project_root)

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
            "Switch to that session or have it release the task first.\n"
            f"If pane {pane} is actually gone, run `endless-go tmux reset` to "
            "clear the stale session and retry."
        )

    return owned_by_current


_CLAIM_REQUIRES_FORCE: frozenset[str] = frozenset({
    "unverified", "confirmed", "declined", "obsolete", "assumed", "completed",
})


# E-1555: statuses a task can be reopened from. `declined`/`obsolete` carry an
# explicit "we chose not to do this" decision — reversing them is an
# intentional act that should use `task update --status` and `--reason`,
# not a generic reopen. `unverified` is not terminal: it's "implementation done,
# trust pending" — reopening it would discard pending verification rather
# than reactivate completed work.
_REOPENABLE_TERMINAL_STATUSES: frozenset[str] = frozenset({
    "assumed", "confirmed", "completed",
})


# E-1845: statuses from which a material description edit resets a task to
# `untriaged`. These are exactly the pre-work states — no implementation has
# started, so re-deciding what the task IS costs nothing but a second look.
#
# `ready` is deliberately included: approval was granted against the OLD
# description, so a rewrite should require re-approval rather than silently
# inherit it. `underway` is deliberately EXCLUDED: a live session is mid-flight
# and a description tweak must not yank the task out from under it. So are
# `unverified` and every terminal status, where re-triage means nothing.
_DESCRIPTION_RESET_FROM: frozenset[str] = frozenset({
    "untriaged", "unplanned", "submitted", "ready", "revisit",
})


# The statuses that mean "nobody has decided this task is spec-complete yet" —
# the ones from which attaching a plan promotes to `submitted`. The promotion
# itself lives in the Go executor; this is a mirror of its source set
# (`isPreJudgmentStatus`, internal/events/executor.go), needed so `--keep-status`
# can tell whether the promotion is about to fire. tests/tasks/e-1913-verify.sh
# asserts the two stay in sync.
_PRE_JUDGMENT_STATUSES: frozenset[str] = frozenset({
    "untriaged", "unplanned",
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
    # the task's status is left untouched rather than stranded underway.
    project_root = _project_root()
    slug_source = title or "task"
    wt_path, created = create_task_worktree(item_id, slug_source, project_root)

    if current_status != "underway":
        emit_event(
            kind="task.status_changed",
            project=proj_name,
            entity_type="task",
            entity_id=str(item_id),
            payload={
                "old_status": current_status,
                "new_status": "underway",
            },
            session_id=session_id_arg,
        )
        _emit_field_changes(
            item_id, title,
            [("status", current_status, "underway")],
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
      - Bypasses the done-ish status gate (unverified/confirmed/declined/
        obsolete/assumed/completed → underway demotion)
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
            f"would demote it to 'underway'.\n"
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

    # A background session may only pick up human-approved (`ready`) work.
    # `ready` provably means approved (unplanned → submitted → approve → ready),
    # so a background loop must not claim tasks still pending approval.
    if (
        current_status != "ready"
        and _session_is_background(target_session)
    ):
        raise click.ClickException(
            f"A background session may only claim 'ready' work; this task is "
            f"'{current_status}'. It must be approved (reach 'ready') before a "
            "background session can pick it up."
        )

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

    Use when the task is already in `assumed` / `confirmed` / `unverified`
    and the user wants the status row to keep showing it as context.
    `claim --force` is the wrong tool there because it demotes status
    back to `underway`.

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


def _clear_revisit_gate(cleared_by: str) -> int:
    """Clear the current session's open revisit gate via the Go helper.

    Shells out to `endless-go session-query gate-clear`, which performs the
    direct session_gates write (no Python DB write, per E-1486 / E-1542).
    Returns the number of open rows cleared (0 = nothing pending). Raises if
    the current session id can't be resolved or the helper fails.
    """
    import subprocess
    from endless import config
    from endless.event_bridge import _resolve_endless_go

    current_eid = _current_endless_session_id()
    if current_eid is None:
        raise click.ClickException(
            "Cannot resolve current session id "
            "(set ENDLESS_SESSION_ID or run inside a tmux pane with a "
            "known companion file)."
        )
    args = [
        _resolve_endless_go(), *config.go_db_context_args(),
        "session-query", "gate-clear",
        "--session-id", str(current_eid),
        "--kind", "revisit",
        "--cleared-by", cleared_by,
    ]
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode != 0:
        raise click.ClickException(
            "gate-clear failed: " + (result.stderr.strip() or "unknown error")
        )
    text = result.stdout.strip()
    return int(text) if text.isdigit() else 0


def continue_item() -> None:
    """Resume under the current plan, clearing the pending revisit prompt.

    Clears the session's open revisit gate (cleared_by='revisit_continue') so
    the PreToolUse hook stops intercepting tool calls. No other side effects.
    """
    if not _clear_revisit_gate("revisit_continue"):
        click.echo("No pending revisit prompt for this session.")
        return
    click.echo(
        click.style("•", fg="cyan")
        + " continuing under the current plan; revisit prompt cleared"
    )


def pause_item() -> None:
    """Pause until the strategy is re-set: clear the prompt and release the task.

    Clears the session's open revisit gate (cleared_by='revisit_pause') and
    then releases the session's claim on its active task (worktree stays
    intact, per release semantics). No-op with a friendly message when no
    revisit prompt is pending.
    """
    if not _clear_revisit_gate("revisit_pause"):
        click.echo("No pending revisit prompt for this session.")
        return
    click.echo(
        click.style("•", fg="cyan")
        + " pausing until the strategy is re-set; revisit prompt cleared"
    )
    release_item(None)


def _reopen_task_core(item_id: int) -> tuple[str, str, bool]:
    """Reopen a terminal-status task back to `ready` or `unplanned`.

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
    new_status = "ready" if text_present else "unplanned"

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
    text: str | None = None,
    parent_id: int | None = None,
    phase: str | None = None,
    tier: int | None = None,
    task_type: str | None = None,
    analysis: str | None = None,
    outcome: str | None = None,
    force: bool = False,
    justification: str | None = None,
    keep_status: bool = False,
):
    """Update fields on a task."""
    from endless.event_bridge import emit_event

    _reject_status_with_keep_status(status, keep_status)
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
    # Use the incoming --type if set in this same update, else the existing type.
    effective_type_for_outcome = task_type if task_type is not None else row[0]["type"]
    _require_outcome_for_completed(status, effective_type_for_outcome, effective_outcome)
    if title is not None:
        validate_title(title, force=force)

    if description is not None:
        validate_description(description)

    # Validate status if provided
    if status is not None:
        valid = ("untriaged", "unplanned", "ready", "underway",
                 "unverified", "confirmed", "assumed", "completed",
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
        # Use the incoming task_type if --type is also being set in this
        # update, else the existing type on the row.
        effective_type = task_type if task_type is not None else row[0]["type"]
        _require_completable_verb_for_completed(
            status, effective_title, effective_type
        )
        # E-1577/E-1579: research/epic tasks reject 'unverified'/'assumed'/
        # 'confirmed'; their only type-specific terminal is 'completed'.
        _require_status_allowed_for_type(status, effective_type)

    # Reject a maybe-phase task gaining (or keeping) a parent. Only evaluate
    # when this update touches phase or parent_id — an unrelated edit must not
    # be blocked just because the row already violates (the pre-rule data set
    # stays editable). Evaluate the *effective* state: the incoming flag wins,
    # else the existing row value. PARENT_NONE (0) means "make root" → None.
    # This single check covers `update <maybe> --parent X` and
    # `update <child> --phase maybe`, while the atomic `--phase next --parent Y`
    # promote+parent combo stays legal.
    if phase is not None or parent_id is not None:
        effective_phase = phase if phase is not None else row[0]["phase"]
        if parent_id is not None:
            effective_parent = None if parent_id == PARENT_NONE else parent_id
        else:
            effective_parent = row[0]["parent_id"]
        _reject_maybe_with_parent(effective_phase, effective_parent)

    # E-1762: auto-reopen a done task whose plan text is actually edited.
    # Editing tasks.text on a done task adds unshipped scope, so the done-status
    # becomes a lie and the task stays hidden from session monitor. Flip it to
    # `revisit` so the reopened work is honestly signaled and re-surfaces. Guards:
    #   - only a REAL text change (identical re-write is a no-op),
    #   - only from a completed-successfully status (reuse the reopen set;
    #     obsolete/declined are deliberate decisions, not revivals),
    #   - an explicit --status in the same update wins (intent), as does
    #     --keep-status (typo/formatting-only edit),
    #   - epics are excluded: their only done-state is `completed`, and flipping
    #     an epic to revisit would trip the E-1542 pause gate for every in-flight
    #     descendant session — too blunt for a plan tweak (flip by hand instead).
    auto_revisit_type = task_type if task_type is not None else row[0]["type"]
    text_changed = text is not None and text != (row[0]["text"] or "")
    auto_revisit = (
        not keep_status
        and status is None
        and text_changed
        and auto_revisit_type != "epic"
        and row[0]["status"] in _REOPENABLE_TERMINAL_STATUSES
    )

    # E-1845: a material description edit resets a pre-work task to `untriaged`.
    # The description IS the spec that triage (untriaged → unplanned/submitted)
    # and approval (submitted → ready) were judged against, so rewriting it
    # invalidates those judgments — the task has to be looked at again. Guards
    # mirror E-1762's auto-revisit above:
    #   - only a REAL change (an identical re-write is a no-op),
    #   - an explicit --status in the same update wins (intent), as does
    #     --keep-status (typo/formatting-only edit),
    #   - only from the pre-work statuses in _DESCRIPTION_RESET_FROM. `ready` IS
    #     included: approval was granted against the old description. `underway`
    #     is deliberately excluded so a description tweak cannot yank work out
    #     from under a live session; so are unverified and every terminal status,
    #     where re-triage would be meaningless.
    description_changed = (
        description is not None
        and description != (row[0]["description"] or "")
    )
    auto_untriage = (
        not keep_status
        and status is None
        and not auto_revisit
        and description_changed
        and row[0]["status"] in _DESCRIPTION_RESET_FROM
    )
    # Composing the reset with the plan-attach promotion. Attaching a non-empty
    # plan in the SAME call answers the triage question the reset would have
    # asked, so the pair lands on `submitted` rather than bouncing to
    # `untriaged` with a full plan attached — which would read as "nobody has
    # looked at this" about a task that was just re-spec'd and planned.
    #
    # This has to be resolved here, not left to the executor's plan-attach
    # auto-move: that move only fires when the update does not set status
    # explicitly, and the reset does set it. Note the reset still costs a `ready`
    # task its approval — correct, since approval was granted against the OLD
    # description; it lands `submitted`, awaiting re-approval.
    plan_attached = text is not None and text.strip() != ""
    untriage_target = "submitted" if plan_attached else "untriaged"

    # E-1913: `--keep-status` holds the status across EVERY auto-transition, not
    # only the two guarded above. The plan-attach promotion (a pre-judgment task
    # + non-empty --text → `submitted`) is the one that used to leak through: it
    # lives in the Go executor, and the flag has no field in the event payload
    # to travel in. So cross the boundary in the vocabulary the executor already
    # speaks — send the current status, and its "caller wins" branch (the same
    # one an explicit --status uses) stands down.
    #
    # Pinned ONLY when the promotion would actually fire. A status field is not
    # inert in the executor: whenever one is present it also rewrites
    # `completed_at` and clears the tier of a terminal-status task, so pinning
    # unconditionally would restamp the completion time of a `confirmed` task
    # whose plan text was merely typo-fixed — the E-1762 case this flag has
    # always served. `status is None` is not re-checked here: passing both
    # --status and --keep-status was rejected at the top of this function.
    keep_status_pin = (
        keep_status
        and plan_attached
        and row[0]["status"] in _PRE_JUDGMENT_STATUSES
    )

    # Build the fields map for the event payload, plus an ordered list of
    # (name, old, new) tuples for change-output rendering.
    fields = {}
    changes: list = []

    def _add(name: str, new_value):
        fields[name] = new_value
        changes.append((name, row[0][name], new_value))

    if status is not None:
        _add("status", status)
    elif auto_revisit:
        _add("status", "revisit")
    elif auto_untriage:
        _add("status", untriage_target)
    elif keep_status_pin:
        # Deliberately not _add(): this writes back the status the row already
        # has, purely to make the executor stand down. Nothing changed, so
        # rendering "Status: unplanned -> unplanned" would misreport the update.
        fields["status"] = row[0]["status"]

    if phase is not None:
        _add("phase", phase)

    if title is not None:
        _add("title", title)

    if description is not None:
        _add("description", description)

    if text is not None:
        _add("text", text)
        _mirror_plan_to_worktree(item_id, text)

    if parent_id is not None:
        _add("parent_id", parent_id if parent_id > 0 else None)

    if tier is not None:
        if tier == TIER_CLEAR:
            _add("tier", None)
        else:
            _add("tier", tier)
            # Tier 1 tasks are exempt from planning — and (E-1845) from triage
            # too — so auto-advance either pre-work status to ready. E-1913:
            # --keep-status suppresses this the same as the other three
            # auto-transitions; the flag means no inferred status change, and
            # "which tier is this" is a separate question from "is it approved".
            if (
                tier == 1
                and status is None
                and not keep_status
                and row[0]["status"] in _PRE_JUDGMENT_STATUSES
            ):
                _add("status", "ready")

    if outcome is not None:
        _add("outcome", outcome)
        if outcome.strip():
            _mirror_doc_to_worktree(item_id, "outcomes", "outcome", outcome)

    if task_type is not None:
        valid_types = ("todo", "bugfix", "research", "epic", "brainstorm")
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
        if analysis.strip():
            _mirror_doc_to_worktree(item_id, "analyses", "analysis", analysis)

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

    # E-1762: explain the auto-flip. The field render above already shows
    # `Status: <old> -> revisit`; this line names WHY and the escape hatch.
    if auto_revisit:
        click.echo(
            f"{task_id_display(item_id)} was '{row[0]['status']}'; plan text "
            f"changed → status set to revisit "
            f"(pass --keep-status to suppress for a typo/formatting-only edit)."
        )

    # E-1845: same shape as the auto-revisit note above — the field render
    # already shows `Status: <old> -> untriaged`; this names WHY and the hatch.
    if auto_untriage:
        because = (
            "re-spec'd and re-planned in one call"
            if plan_attached
            else "the description is the spec that triage and approval were "
                 "judged against"
        )
        click.echo(
            f"{task_id_display(item_id)} was '{row[0]['status']}'; description "
            f"changed → status set to {untriage_target} "
            f"({because}; pass --keep-status to suppress for a "
            f"typo/formatting-only edit)."
        )

    # E-1772: nudge toward `endless task report` on an agent's wind-down. Only
    # when this update actually set a status; effective_outcome covers the
    # "outcome authored earlier, status flipped now" workflow the same way the
    # completed-gate above does.
    if status is not None:
        _maybe_emit_report_reminder(
            item_id, row[0]["status"], status, bool(effective_outcome and effective_outcome.strip())
        )


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


def _echo_field_placeholder(label, val, name, content, show, flag):
    """One-line `Name: N chars (--flag to display)` for a large field that is
    present but hidden. Rendered with the header `label`/`val` styling so it
    groups with the other single-line `Label: value` fields above Description.
    Nothing when the field is empty or is being shown in full below (E-1601)."""
    if not content or show:
        return
    click.echo(
        f"{label(name)} {val(f'{len(content)} chars')} "
        + click.style(f"({flag} to display)", dim=True)
    )


class _ColorProxy(io.TextIOBase):
    """A stdout stand-in whose `isatty()` returns a fixed value, so click.echo's
    ANSI auto-strip follows an explicit color decision instead of whether the
    real destination is a terminal (E-1746).

    Under `--paged` the real destination is a pipe to `less` (isatty() false),
    yet a terminal sits on the far side of the pager, so color must be forced
    ON — the git-pager model. Wrapping the pager pipe in a proxy that reports
    `isatty()==True` makes every downstream `click.echo` preserve its color
    without threading a `color=` argument through ~30 call sites. Conversely,
    `--no-color` on a real TTY sets the proxy's tty to False so header/label
    colors are stripped too, not just the markdown fields."""

    def __init__(self, dest, tty: bool):
        self._dest = dest
        self._tty = tty

    def write(self, s):
        return self._dest.write(s)

    def flush(self):
        try:
            self._dest.flush()
        except Exception:
            pass

    def isatty(self):
        return self._tty


def _render_markdown_field(content: str) -> str | None:
    """Colorize markdown `content` to ANSI via `endless-go markdown render`
    (E-1746). Returns the rendered ANSI, or None if the binary can't be resolved
    or the render fails — the caller then falls back to the plain text so
    `task show` never breaks on a rendering hiccup."""
    from endless.event_bridge import _resolve_endless_go
    try:
        binary = _resolve_endless_go()
    except click.ClickException:
        return None
    # Width for table layout. get_terminal_size honors COLUMNS and the
    # controlling tty even when our stdout is a pipe to `less` (E-1775).
    width = shutil.get_terminal_size().columns
    try:
        result = subprocess.run(
            [binary, "markdown", "render", "--width", str(width)],
            input=content, capture_output=True, text=True, timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return result.stdout


def _echo_field_body(content: str, color: bool):
    """Emit a large field's body — colorized markdown when `color`, else the raw
    plain text (E-1746). Rendering failures degrade to plain text."""
    if color:
        rendered = _render_markdown_field(content)
        if rendered is not None:
            # rstrip: the renderer's trailing newline plus the caller's own
            # blank-line framing would otherwise double up.
            click.echo(rendered.rstrip("\n"))
            return
    click.echo(content)


def _echo_large_section(title: str, content: str | None, show: bool, color: bool = False):
    """Multi-line `— Title —` section carrying the full body, shown only when
    its flag is set; the hidden form is the single-line placeholder grouped with
    the header fields (see `_echo_field_placeholder`). Nothing when empty or
    gated off (E-1601). When `color`, the body is rendered as colorized markdown
    (E-1746)."""
    if not content or not show:
        return
    click.echo()
    click.echo(click.style(f"— {title} —", fg="cyan"))
    _echo_field_body(content, color)


def detail_item(
    item_id: int,
    show_description: bool = True,
    show_analysis: bool = False,
    show_text: bool = False,
    show_children: bool = False,
    show_outcome: bool = False,
    llm: bool = False,
    as_json: bool = False,
    paged: bool = False,
    no_color: bool = False,
):
    """Show full detail for a task.

    `show_children` lists EVERY direct child, in all three render paths (JSON,
    llm, human). It used to exclude `status = 'confirmed'` — and only that one
    status, so an epic rendered its `obsolete` and `declined` children while
    dropping the ones that were verified and landed. E-1906 vanished from
    `task show E-1785 --children` that way while four obsolete children stayed,
    which inverts what an epic nearing completion needs to see: the confirmed
    children ARE the progress (E-1911).
    """
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
    # Session provenance (E-1866): who created the task and who else touched it.
    # Resolved once here so all three output modes report the same facts.
    touches = _session_touches(item_id)
    creator = _creating_session(touches)

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
            # Session provenance (E-1866). `created_by` is null for a task filed
            # outside any session; `touched_by` is ordered most-recent-touch-first,
            # matching the human block.
            "created_by": _session_json(creator) if creator else None,
            "touched_by": [_session_json(t) for t in touches],
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
            "outcome": item["outcome"] if show_outcome else None,
            "description": item["description"] if show_description else None,
            "analysis": item["analysis"] if show_analysis else None,
            "text": item["text"] if show_text else None,
            # Char counts are always present (independent of the show flags) so
            # the JSON shape is stable and a consumer can see a hidden field's
            # size without pulling its full body (E-1601).
            "analysis_chars": len(item["analysis"]) if item["analysis"] else None,
            "text_chars": len(item["text"]) if item["text"] else None,
            "outcome_chars": len(item["outcome"]) if item["outcome"] else None,
        }
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? "
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
        if item["parent_id"]:
            click.echo(f"parent=E-{item['parent_id']}")
        links = _flatten_relations(item_id)
        if links:
            link_str = ",".join(f"E-{r['id']} ({r['rel']})" for r in links)
            click.echo(f"links={link_str}")
        click.echo(f"created={item['created_at']}")
        # Session provenance (E-1866). Each entry mirrors the human block's row
        # order — relation, session, its active task, state — so the two read the
        # same: 'revisited ES-996 (E-1833) [idle]'. Most recent touch first.
        if creator:
            click.echo(f"created_by={_session_ref(creator)}")
        if touches:
            touch_str = ",".join(
                f"{t['rel_slug'] or 'touched'} {_session_ref(t)} [{t['state']}]"
                for t in touches
            )
            click.echo(f"touched_by={touch_str}")
        click.echo(f"updated={item['updated_at']}")
        if item["completed_at"]:
            click.echo(f"confirmed={item['completed_at']}")
        if landings:
            click.echo(f"landed={_format_landed_line(landings)}")
        # Large fields collapse to a char marker unless their flag is set, so
        # `task show --llm` stays token-cheap on tasks whose outcome is a large
        # deliverable; pass --outcome/--text/--analysis to pull the body (E-1601).
        for name, content, shown in (
            ("analysis", item["analysis"], show_analysis),
            ("text", item["text"], show_text),
            ("outcome", item["outcome"], show_outcome),
        ):
            if content and not shown:
                click.echo(f"{name}_chars={len(content)}")
        if show_description and item["description"] and item["description"] != item["title"]:
            click.echo(f"\n## Description\n{item['description']}")
        if show_analysis and item["analysis"]:
            click.echo(f"\n## Analysis\n{item['analysis']}")
        if show_text and item["text"]:
            click.echo(f"\n## Text\n{item['text']}")
        if show_outcome and item["outcome"]:
            click.echo(f"\n## Outcome\n{item['outcome']}")
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM tasks WHERE parent_id = ? "
                "ORDER BY id",
                (item_id,),
            )
            if children:
                click.echo("\n## Children")
                for c in children:
                    click.echo(f"E-{c['id']} {c['phase']} {c['status']} {c['title']}")
        return

    # Human-readable output. Color decision (E-1746): honor --no-color; else
    # color when stdout is a TTY, OR when we spawn the pager ourselves — a real
    # terminal sits on the far side of `less`, so the pipe's isatty()==false
    # must not disable color (the git-pager model; the trap glow falls into).
    color = (not no_color) and (sys.stdout.isatty() or paged)

    pager = None
    if paged:
        pager = subprocess.Popen(
            # -R preserve color, --mouse wheel scroll, -F quit if it fits one
            # screen. No -X (can suppress mouse-init; less 668 doesn't need it).
            ["less", "-R", "--mouse", "-F"],
            stdin=subprocess.PIPE, text=True,
        )
        dest = pager.stdin
    else:
        dest = sys.stdout

    try:
        # Route the whole render through a proxy whose isatty()==color so every
        # click.echo strips/keeps ANSI per the decision above, with no color=
        # argument threaded through the ~30 header/section call sites.
        with contextlib.redirect_stdout(_ColorProxy(dest, color)):
            _render_detail_human(
                item, landings, item_id,
                show_description=show_description,
                show_analysis=show_analysis,
                show_text=show_text,
                show_children=show_children,
                show_outcome=show_outcome,
                color=color,
                touches=touches,
                creator=creator,
            )
    finally:
        if pager is not None:
            try:
                pager.stdin.close()
            except BrokenPipeError:
                pass
            pager.wait()


def _render_detail_human(
    item, landings, item_id,
    show_description: bool,
    show_analysis: bool,
    show_text: bool,
    show_children: bool,
    show_outcome: bool,
    color: bool,
    touches: list[dict],
    creator: dict | None,
):
    """Emit the human-readable `task show` detail to the current stdout. Split
    from detail_item so the whole render can run under a color/pager proxy
    (E-1746). Multiline markdown fields (description/analysis/text/outcome) are
    colorized when `color`. `touches`/`creator` are the session provenance
    detail_item already resolved for every output mode (E-1866)."""
    col_w = 11  # width of label column (longest: "Confirmed:" = 10 + 1 space)
    label = lambda s: click.style(f"{s:<{col_w}}", fg="cyan")
    val = lambda s: click.style(str(s), fg="white", bold=True)

    # Both multi-line bullet blocks are measured up front so they can share one
    # label column and read as siblings rather than two ragged lists (E-1866).
    links = _flatten_relations(item_id)
    bullet_w = max(
        _bullet_label_width(r["rel_label"] for r in links),
        _bullet_label_width(t["rel_label"] for t in touches),
    )

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
    created_line = f"{label('Created:')} {val(_format_timestamp(item['created_at']))}"
    if creator:
        created_line += click.style(" by ", dim=True) + val(_session_ref(creator))
    click.echo(created_line)
    if item["updated_at"] and item["updated_at"] != item["created_at"]:
        click.echo(f"{label('Updated:')} {val(_format_timestamp(item['updated_at']))}")
    if item["completed_at"]:
        click.echo(f"{label('Confirmed:')} {val(_format_timestamp(item['completed_at']))}")
    if landings:
        click.echo(f"{label('Landed:')} {val(_format_landed_line(landings))}")
    if item["source_file"]:
        click.echo(f"{label('Source:')} {val(item['source_file'])}")
    # A hidden large field collapses to a single-line `Name: N chars` placeholder
    # grouped here with the other Label: value fields; its full body (when the
    # matching flag is set) renders as a multi-line section after Description
    # (E-1601). Analysis precedes Text: pre-plan design content (E-999).
    _echo_field_placeholder(label, val, "Analysis:", item["analysis"], show_analysis, "--analysis")
    _echo_field_placeholder(label, val, "Text:", item["text"], show_text, "--text")
    _echo_field_placeholder(label, val, "Outcome:", item["outcome"], show_outcome, "--outcome")
    # Links last: multi-line blocks sit below the single-line fields (E-1477).
    # 'Touched by:' follows 'This task:' as its session-side peer (E-1866).
    _echo_links_section(item_id, min_width=bullet_w, links=links)
    _echo_touched_by_section(touches, min_width=bullet_w)

    # Multi-line sections after Description: Description first, then the full
    # bodies of any large field whose flag is set (otherwise its placeholder
    # showed above).
    if show_description and item["description"] and item["description"] != item["title"]:
        click.echo()
        click.echo(click.style("— Description —", fg="cyan"))
        _echo_field_body(item["description"], color)

    _echo_large_section("Analysis", item["analysis"], show_analysis, color)
    _echo_large_section("Text", item["text"], show_text, color)
    _echo_large_section("Outcome", item["outcome"], show_outcome, color)

    if show_children:
        children = db.query(
            "SELECT id, COALESCE(title, description) as title, status, phase "
            "FROM tasks WHERE parent_id = ? "
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


_HANDOFF_TYPES = frozenset({"todo", "bugfix", "research", "epic", "brainstorm"})

# Terminal statuses collapse into a single "terminal" bucket in the
# children-state breakdown (E-1567). Covers every status a finished child
# can hold: todo/bugfix land on confirmed/assumed, research/epic land on
# completed (E-1577/E-1537 §3), and obsolete/declined are universal
# terminals.
_TERMINAL_STATUSES = frozenset(
    {"confirmed", "assumed", "completed", "declined", "obsolete"}
)

# Display order for the children-state breakdown: lifecycle progression of
# the in-flight statuses, then the collapsed terminal bucket last. Every
# valid task status maps to one of these buckets so no child is silently
# dropped and the "(N total)" suffix always reconciles with the child count.
_CHILDREN_STATE_ORDER = (
    "untriaged",
    "unplanned",
    "ready",
    "underway",
    "blocked",
    "revisit",
    "unverified",
    "terminal",
)


def _children_state(parent_id: int) -> str:
    """Return a pre-formatted children-state breakdown for an epic handoff.

    Groups the task's direct children by status, collapses all terminal
    statuses into a single ``terminal`` bucket, and renders the non-empty
    buckets in lifecycle order followed by a total, e.g.
    ``"2 unplanned, 3 ready, 1 underway, 4 terminal (10 total)"``.
    A single bucket still gets the total: ``"3 ready (3 total)"``. With no
    children it returns ``"no children yet"``. See E-1567.
    """
    rows = db.query(
        "SELECT status, count(*) AS n FROM tasks WHERE parent_id = ? "
        "GROUP BY status",
        (parent_id,),
    )
    counts: dict[str, int] = {}
    total = 0
    for row in rows:
        status = row["status"]
        n = row["n"]
        total += n
        bucket = "terminal" if status in _TERMINAL_STATUSES else status
        counts[bucket] = counts.get(bucket, 0) + n
    if total == 0:
        return "no children yet"
    parts = [
        f"{counts[bucket]} {bucket}"
        for bucket in _CHILDREN_STATE_ORDER
        if counts.get(bucket)
    ]
    return f"{', '.join(parts)} ({total} total)"


def render_handoff(spawned_id: int, title: str,
                   worktree_path: str | None = None,
                   branch: str | None = None,
                   task_type: str | None = None,
                   parent_id: int | None = None,
                   bg: bool = False,
                   respawn: bool = False,
                   restore_case: str | None = None,
                   prior_outcome: str | None = None,
                   last_status_snapshot: str | None = None) -> str:
    """Render the spawn handoff for a task by invoking `endless-go template render`.

    The handoff is mostly boilerplate (orient, read the guide + plan, default
    interaction rules, closing); only the task id, title, and the task's
    worktree/branch vary. Generating it means
    agents no longer author prompts, so prompt-vs-plan drift cannot occur.
    See E-1469. E-1565 moved the rendering surface from Python's
    string.Template to Go's text/template — Python builds the var map and
    shells out per render. E-1566 split the single template into
    per-type variants under `handoff/{todo,bugfix,research,epic}.md.tmpl`;
    unknown or null `task_type` falls back to the todo variant. The child
    count is universal — per E-1552, every variant includes a conditional
    line naming the count when nonzero.

    `bg=True` (E-1568) renders the background-agent variant of each template:
    a headless `claude --bg` agent tells the agent to do the work, flip the
    task to `unverified`, and stop (the user attaches later via
    `claude attach <short_id>`).

    `respawn=True` (E-1647) renders the flat, type-agnostic `handoff/respawn`
    template used when a task is *reopened*. The four per-type templates are
    initial-spawn instructions that drive toward an end-state; a reopened
    session has no defined end-state yet, so it gets a distinct interrogative
    handoff. The reopen path (E-1645) supplies three extra vars carried as
    read-only restore context: `restore_case`
    (`reused` | `rebuilt-off-main` | `recovered-post-drop`), the task's
    `prior_outcome`, and `last_status_snapshot` (rendered markdown of the
    latest `session_statuses` row). Any may be empty.
    """
    import json
    import subprocess
    from endless.event_bridge import _resolve_endless_go

    effective_type = task_type if task_type in _HANDOFF_TYPES else "todo"
    child_rows = db.query(
        "SELECT count(*) AS n FROM tasks WHERE parent_id = ?",
        (spawned_id,),
    )
    child_count = child_rows[0]["n"] if child_rows else 0

    vars_payload = {
        "spawned_id": spawned_id,
        "label_prefix": _hierarchical_label_prefix(spawned_id, parent_id),
        "title": title,
        # E-1822: the shared handoff/_mechanics partials branch on task_type,
        # so the type must travel as a var — not only as the choice of which
        # per-type template to render. `effective_type` (not the raw arg) so an
        # unknown/absent type resolves to the same `todo` the template pick does.
        "task_type": effective_type,
        "worktree_path": worktree_path or "<task worktree>",
        "branch": branch or "<task branch>",
        "child_count": child_count,
        "children_state": _children_state(spawned_id),
        "bg": bg,
    }
    if respawn:
        vars_payload["restore_case"] = restore_case or "reused"
        vars_payload["prior_outcome"] = prior_outcome or ""
        vars_payload["last_status_snapshot"] = last_status_snapshot or ""
    template_name = "handoff/respawn" if respawn else f"handoff/{effective_type}"
    binary = _resolve_endless_go()
    result = subprocess.run(
        [binary, "template", "render", template_name],
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
        "SELECT t.id, t.title, t.parent_id, COALESCE(tt.slug, '') AS type_slug "
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
        worktree_path=str(wt) if wt else None,
        branch=_branch_for_worktree(wt) if wt else None,
        task_type=row[0]["type_slug"] or None,
        parent_id=row[0]["parent_id"],
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


def _resolve_live_owner(item_id: int) -> dict | None:
    """Return navigation info for a live session that owns the task, or None.

    The reopen path navigates to an existing live owner instead of double-
    spawning (E-1645). A DB row with `state != 'ended'` is only treated as a
    live owner if it also appears in the actually-live set (tmux/process check
    via `_live_sessions`) — a stale `working` row left by a ghost (E-1640) is
    NOT a live owner and the caller proceeds to spawn.

    Returns `{"eid": int, "target": "fg", "pane_id": "%NN"}` for a foreground
    (tmux-pane) owner, `{"eid": int, "target": "bg"}` for a background agent, or
    None when no session is genuinely live for the task.
    """
    rows = db.query(
        "SELECT id AS eid FROM sessions "
        "WHERE active_task_id = ? AND state != 'ended'",
        (item_id,),
    )
    if not rows:
        return None

    from endless.session_cmd import _live_sessions, _project_root_for_cwd
    live = _live_sessions(_project_root_for_cwd())
    live_by_eid = {
        c["endless_session_id"]: c
        for c in live
        if isinstance(c.get("endless_session_id"), int)
    }
    for r in rows:
        comp = live_by_eid.get(r["eid"])
        if comp is None:
            continue
        pane = comp.get("pane_id") or ""
        if pane:
            return {"eid": r["eid"], "target": "fg", "pane_id": pane}
        return {"eid": r["eid"], "target": "bg"}
    return None


def _fetch_reopen_context(item_id: int) -> dict:
    """Fetch the read-only restore context for a reopen via the Go resolver.

    Shells out to `endless-go session-query reopen-context` so the inherited-
    session pick and the snapshot render happen Go-side (no Python DB read —
    E-894 / E-1486). Returns a dict with keys `inherited_session_id` (int, 0 =
    none), `prior_outcome` (str), `last_status_snapshot` (str). On any failure
    the context degrades to empty rather than aborting the reopen — a missing
    snapshot is cosmetic, not load-bearing.
    """
    import json
    import subprocess
    from endless import config
    from endless.event_bridge import _resolve_endless_go

    empty = {
        "inherited_session_id": 0,
        "prior_outcome": "",
        "last_status_snapshot": "",
    }
    try:
        binary = _resolve_endless_go()
        result = subprocess.run(
            [binary, *config.go_db_context_args(),
             "session-query", "reopen-context", "--task-id", str(item_id)],
            capture_output=True, text=True,
        )
    except OSError as e:
        click.echo(f"  warning: reopen-context lookup failed: {e}", err=True)
        return empty
    if result.returncode != 0:
        click.echo(
            "  warning: reopen-context lookup failed: "
            f"{(result.stderr or result.stdout).strip()}",
            err=True,
        )
        return empty
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return empty
    return {
        "inherited_session_id": int(data.get("inherited_session_id") or 0),
        "prior_outcome": data.get("prior_outcome") or "",
        "last_status_snapshot": data.get("last_status_snapshot") or "",
    }


def _resolve_reopen_decision(item_id: int, new_session: bool) -> dict:
    """Compute the reopen decision for a task — READ-ONLY (no mutations).

    Both the `--print-decision` seam and the live spawn path call this so the
    printed decision matches what actually happens. It performs only reads: the
    liveness lookup and the Go reopen-context resolver. It does NOT flip status,
    create a worktree, or launch anything.

    Returns a dict:
      - `kind`: "navigate" (a live owner exists) or "spawn".
      - `navigate`: the `_resolve_live_owner` dict, or None.
      - `restore_case`: predicted "reused" (canonical worktree present) or
        "rebuilt-off-main" (reaped — will be recreated off main).
      - `worktree_path`: the canonical worktree path (str).
      - `session_mode`: "new-session" or "inherit".
      - `inherit_session_id`: int | None (None for new-session / no prior).
      - `prior_outcome`, `last_status_snapshot`: read-only restore context.
    """
    from endless.worktree_cmd import _project_root

    nav = _resolve_live_owner(item_id)
    if nav is not None:
        return {"kind": "navigate", "navigate": nav}

    wt_dir = _project_root() / ".endless" / "worktrees" / f"e-{item_id}"
    restore_case = "reused" if wt_dir.exists() else "rebuilt-off-main"

    ctx = _fetch_reopen_context(item_id)
    if new_session:
        session_mode = "new-session"
        inherit_session_id = None
        last_status_snapshot = ""
    else:
        inherit_session_id = ctx["inherited_session_id"] or None
        session_mode = "inherit"
        last_status_snapshot = ctx["last_status_snapshot"]

    return {
        "kind": "spawn",
        "navigate": None,
        "restore_case": restore_case,
        "worktree_path": str(wt_dir),
        "session_mode": session_mode,
        "inherit_session_id": inherit_session_id,
        "prior_outcome": ctx["prior_outcome"],
        "last_status_snapshot": last_status_snapshot,
    }


def _print_reopen_decision(item_id: int, decision: dict) -> None:
    """Print a reopen decision in a stable, greppable form (the seam output)."""
    if decision["kind"] == "navigate":
        nav = decision["navigate"]
        if nav["target"] == "fg":
            click.echo(
                f"reopen decision for {task_id_display(item_id)}: navigate "
                f"(foreground) — tmux switch-client -t {nav['pane_id']}"
            )
        else:
            click.echo(
                f"reopen decision for {task_id_display(item_id)}: navigate "
                f"(background) — attach with: endless task attach "
                f"{task_id_display(item_id)}"
            )
        return
    if decision["session_mode"] == "new-session":
        session_line = "session: new-session"
    else:
        sid = decision["inherit_session_id"]
        session_line = (
            f"session: inherit-session={sid}" if sid
            else "session: inherit (no prior session)"
        )
    click.echo(f"reopen decision for {task_id_display(item_id)}: spawn")
    click.echo(f"  restore_case={decision['restore_case']}")
    click.echo(f"  {session_line}")
    click.echo(f"  worktree={decision['worktree_path']}")


def _navigate_to_live_owner(nav: dict) -> None:
    """Switch to a live foreground owner's pane (E-1645).

    Foreground: `tmux switch-client -t <pane>` — works across tmux clients
    (unlike `select-window`). Background: nothing to do here; the printed
    `endless task attach` line is the user's action.
    """
    import subprocess

    if nav["target"] != "fg":
        return
    pane = nav["pane_id"]
    if not os.environ.get("TMUX"):
        click.echo(
            f"  (not in a tmux client — switch manually: "
            f"tmux switch-client -t {pane})",
            err=True,
        )
        return
    subprocess.run(["tmux", "switch-client", "-t", pane], check=False)


def spawn_plan(item_id: int, project_name: str | None = None,
               worktree: str | None = None, force: bool = False,
               reopen: bool = False, bg: bool = False, attach: bool = False,
               new_session: bool = False, print_decision: bool = False,
               permission_mode: str = "auto", model: str | None = None,
               name: str | None = None):
    """Spawn a new tmux window with Claude working on a task's prompt.

    Pre-claims the task (status flip + worktree creation) BEFORE launching
    Claude, so the spawned session lands in a worktree on a task that is
    already underway. The SessionStart hook reads
    `@endless_spawned_by` from the new tmux window and records the
    session→task binding via `BindSessionToTask` (no redundant status
    flip). See E-1274.

    `reopen=True` (E-1555) reopens an `assumed`/`confirmed`/`completed`
    target as a pre-step (status → `ready`/`unplanned` based on text
    presence) before proceeding with spawn. Errors on non-terminal or
    decision-bearing (`declined`/`obsolete`) statuses.

    The foreground path launches Claude as the tmux window's *command* via the
    `endless-go spawn-window` launcher (E-1705): the handoff is delivered as
    claude's positional prompt argument, not typed in with send-keys. Spawned
    sessions default to `--permission-mode auto` (override with `permission_mode`;
    `model`/`name` are optional claude pass-throughs). There is no plan-mode step
    — a positional prompt leaves no interactive turn to type a slash-command into.

    `bg=True` (E-1568) dispatches the agent headless via `claude --bg --name
    E-<id>` instead of a tmux window. No tmux is required; the same done-ish
    gate, pre-claim, and worktree creation run first. The dispatch row is
    written with session_id NULL + the short id parsed from `claude --bg`
    stdout; the agent's SessionStart hook fills in the real UUID later.

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
            "status to ready/unplanned (handoff intent), --force demotes "
            "to underway (self-pickup intent). Pick one."
        )

    # --new-session and --print-decision are reopen-path modifiers (E-1645):
    # session inheritance and the decision seam only exist for a reopen.
    if new_session and not reopen:
        raise click.ClickException(
            "--new-session only applies with --reopen (it opts out of "
            "inheriting the prior session's restore context)."
        )
    if print_decision and not reopen:
        raise click.ClickException(
            "--print-decision only applies with --reopen (it prints the "
            "resolved reopen decision without spawning)."
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
        "SELECT p.id, p.title, p.status, p.project_id, p.parent_id, "
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
        # Open the attach window via the endless-go launcher (E-1705): it runs
        # `claude attach <short-id>` as the window command and records the
        # diagnostic @endless_attached_short_id option — no send-keys.
        from endless.event_bridge import _resolve_endless_go
        binary = _resolve_endless_go()
        subprocess.run(
            [binary, "spawn-window", "--attach",
             "--short-id", short_id,
             "--claude-bin", _claude_binary(),
             "--window-name", window_name,
             "--cwd", os.getcwd()],
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

    # E-1645: reopen path — liveness guard (navigate instead of double-spawn),
    # the read-only decision seam, then the E-1555 reopen pre-step. reopen_render
    # carries the respawn-handoff restore context (stays None for a non-reopen
    # spawn, which keeps the per-type initial-spawn handoff).
    reopen_render: dict | None = None
    if reopen:
        # Liveness guard FIRST, before any mutation: if a session is genuinely
        # live for the task, navigate to it rather than spawn a duplicate.
        # --new-session does NOT bypass a live owner.
        decision = _resolve_reopen_decision(item_id, new_session=new_session)
        if decision["kind"] == "navigate":
            _print_reopen_decision(item_id, decision)
            if not print_decision:
                _navigate_to_live_owner(decision["navigate"])
            return

        # No live owner: the reopen is only valid from a terminal status.
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

        # Read-only seam: print the resolved decision and stop. No status flip,
        # no worktree creation, no launch.
        if print_decision:
            _print_reopen_decision(item_id, decision)
            return

        # Carry the read-only restore context into the respawn handoff; the
        # actual restore_case is finalized from `created` after the worktree
        # is ensured below.
        reopen_render = {
            "prior_outcome": decision["prior_outcome"],
            "last_status_snapshot": decision["last_status_snapshot"],
        }

        # Reopen pre-step: flip terminal → ready/unplanned, release any
        # lingering session binding, emit audit event.
        _reopen_task_core(item_id)
        # _perform_claim_work below sees the post-reopen status and
        # promotes ready/unplanned → underway on its own.
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
            f"would demote it to 'underway'.\n"
            "Pass --force to confirm the demotion, or update the status "
            "first if that's not what you intended."
        )

    # Refuse if another live session already owns the task. Passing
    # current_eid=None treats any owner as "other" — spawn never claims
    # ownership for the spawning session. The reopen path already ran its own
    # liveness guard above (it navigates instead of raising), so this
    # raise-on-conflict check is for the non-reopen spawn only.
    if not reopen:
        _check_task_ownership(item_id, current_eid=None)

    # Pre-claim: emit status_changed, create worktree. No session binding
    # yet — Claude hasn't started. SessionStart's @endless_spawned_by
    # path will record the binding once the new session is up.
    _, proj_name = _resolve_project(None)
    wt_path, created = _perform_claim_work(
        item_id=item_id,
        title=title,
        current_status=current_status,
        target_session=None,
        proj_name=proj_name,
    )

    # E-1645: finalize the actual restore case from whether the worktree was
    # freshly created (reaped → rebuilt off main) or reused as-is.
    if reopen_render is not None:
        reopen_render["restore_case"] = (
            "reused" if not created else "rebuilt-off-main"
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
            parent_id=item["parent_id"],
            worktree_override=worktree is not None,
            reopen_render=reopen_render,
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
    # write it to a temp file for tmux load-buffer. cd_target is the worktree
    # the spawned session lands in.
    handoff_text = render_handoff(
        item_id, title,
        worktree_path=cd_target,
        branch=_branch_for_worktree(cd_target),
        task_type=item["type_slug"] or None,
        parent_id=item["parent_id"],
        respawn=reopen_render is not None,
        restore_case=(reopen_render or {}).get("restore_case"),
        prior_outcome=(reopen_render or {}).get("prior_outcome"),
        last_status_snapshot=(reopen_render or {}).get("last_status_snapshot"),
    )
    handoff_file = tempfile.NamedTemporaryFile(
        mode="w", suffix=".md", prefix="endless-handoff-",
        delete=False,
    )
    handoff_file.write(handoff_text)
    handoff_file.close()

    # Launch Claude as the tmux window's command via the endless-go launcher
    # (E-1705). The launcher creates the window, sets the @endless_* options
    # in-process BEFORE exec (so SessionStart's option reads never race the way
    # the old send-keys/sleep timing did), reads the handoff from the temp file
    # and passes it as claude's positional prompt, then deletes the temp file.
    # No send-keys, no plan-mode paste, no readiness sleep, and the handoff text
    # never touches a command line or the session environment.
    from endless.event_bridge import _resolve_endless_go
    binary = _resolve_endless_go()
    spawn_cmd = [
        binary, "spawn-window",
        "--claude-bin", _claude_binary(),
        "--handoff-file", handoff_file.name,
        "--permission-mode", permission_mode,
        "--task-id", str(item_id),
        "--project-id", str(item["project_id"]),
        "--spawned-by", str(spawner_id),
        "--window-name", window_name,
        "--cwd", cd_target,
    ]
    if model:
        spawn_cmd += ["--model", model]
    if name:
        spawn_cmd += ["--name", name]
    subprocess.run(spawn_cmd, check=True)

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


# E-1572: default soft-throttle threshold. Warn once the project already has
# this many bg agents `working`. Configurable per project via
# .endless/config.json:bg_throttle_warn; set to 0 (or any value <= 0) to disable.
_BG_THROTTLE_DEFAULT = 3


def _bg_throttle_warn(item_id: int) -> None:
    """Emit a soft throttle warning to stderr if the project already has at
    least `bg_throttle_warn` background agents `working` (E-1572).

    Advisory only: never blocks dispatch and never raises on its own failure —
    a config or count hiccup must not abort the spawn. Reads the threshold from
    the project config (default 3; <= 0 disables) and the live count from the
    `session-query count-bg-agents` Go helper (no Python DB read, per E-1486).
    """
    import subprocess
    from endless.event_bridge import _resolve_endless_go

    cfg = config.project_config_read(config.resolution_cwd()) or {}
    try:
        threshold = int(cfg.get("bg_throttle_warn", _BG_THROTTLE_DEFAULT))
    except (TypeError, ValueError):
        threshold = _BG_THROTTLE_DEFAULT
    if threshold <= 0:
        return

    binary = _resolve_endless_go()
    res = subprocess.run(
        [binary, *config.go_db_context_args(),
         "session-query", "count-bg-agents", "--task-id", str(item_id)],
        capture_output=True, text=True,
    )
    if res.returncode != 0:
        return
    try:
        active = int(res.stdout.strip())
    except ValueError:
        return
    if active < threshold:
        return

    click.echo(
        f"warning: {active} bg agents already active for this project "
        f"(threshold: {threshold}).",
        err=True,
    )
    click.echo(
        "  Each bg agent consumes a parallel-execution slot; quota burns "
        "~linearly.",
        err=True,
    )
    click.echo(
        "  Community-observed sweet spot is 3–5 parallel agents. (Configure "
        "via .endless/config.json:bg_throttle_warn.)",
        err=True,
    )


def _spawn_bg_dispatch(item_id: int, title: str, cd_target: str,
                       task_type: str | None, parent_id: int | None,
                       worktree_override: bool,
                       reopen_render: dict | None = None):
    """Dispatch a background agent for an already-pre-claimed task (E-1568).

    Renders the bg handoff variant, launches `claude --bg --name <label>` with
    the handoff as a positional argv (well under ARG_MAX), parses the short id
    from stdout, and records the dispatch sessions row (session_id NULL +
    short_id, kind background) via the `session-query record-bg-agent` Go
    helper. The agent's SessionStart hook attaches the real UUID later.

    The `--name` label carries the hierarchical task context (E-1620):
    `E-<parent>/E-<id>: <title>` for a parented task, `E-<id>: <title>` for a
    root, so Agent View rows self-identify by task and parent.
    """
    import subprocess
    from endless import config
    from endless.event_bridge import _resolve_endless_go

    label = f"{_hierarchical_label_prefix(item_id, parent_id)}: {title}"

    handoff_text = render_handoff(
        item_id, title,
        worktree_path=cd_target,
        branch=_branch_for_worktree(cd_target),
        task_type=task_type,
        parent_id=parent_id,
        bg=True,
        respawn=reopen_render is not None,
        restore_case=(reopen_render or {}).get("restore_case"),
        prior_outcome=(reopen_render or {}).get("prior_outcome"),
        last_status_snapshot=(reopen_render or {}).get("last_status_snapshot"),
    )

    # E-1572: soft throttle warning. Count the bg agents already `working` for
    # this project; if the count meets the configured threshold, warn (to
    # stderr — keeps stdout clean for short-id parsing by any caller) but do
    # NOT block. The coordinator decides whether one more is worth it.
    _bg_throttle_warn(item_id)

    claude_bin = _claude_binary()

    try:
        result = subprocess.run(
            [claude_bin, "--bg", "--name", label, handoff_text],
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
        + click.style(label, bold=True)
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
        "SELECT id, parent_id, phase FROM tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"Task {task_id_display(item_id)} not found."
        )

    # A maybe-phase task cannot be moved under a parent. Moving to root
    # (target_parent_id is None) is always fine.
    _reject_maybe_with_parent(row[0]["phase"], target_parent_id)

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

    # Friendly-duplicate pre-check: the Go UNIQUE constraint is the integrity
    # backstop, but its error is a low-level SQLite string. Detect the common
    # case here where we still hold the user's display dep_type and E-NNN
    # formatting, and raise the readable message. (The constraint still guards
    # the rare TOCTOU race.)
    if db.exists(
        "SELECT 1 FROM task_deps WHERE source_type = 'task' AND source_id = ? "
        "AND target_type = 'task' AND target_id = ? AND dep_type = ?",
        (src, tgt, stored),
    ):
        raise click.ClickException(
            f"{task_id_display(source_id)} is already linked to {task_id_display(target_id)} as '{dep_type}'."
        )

    # Emit rather than writing task_deps directly: the Go executor owns the
    # write, so the relation reaches the event pipeline and enrolls both tasks
    # in session_tasks as 'revisited'. src/tgt are already storage (active-voice)
    # order after the swap above; the executor inserts them verbatim.
    from endless.event_bridge import emit_event

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task_dep.created",
        project=proj_name,
        entity_type="task_dep",
        entity_id=str(src),
        payload={"source_id": src, "target_id": tgt, "dep_type": stored},
        prompt_verb="linked for",
    )

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
        # Friendly no-match pre-check: the Go executor errors loudly if no row
        # matches, but with a low-level message. Raise the readable one here
        # while we still hold the user's E-NNN formatting.
        if not db.exists(
            "SELECT 1 FROM task_deps WHERE source_type = 'task' AND source_id = ? "
            "AND target_type = 'task' AND target_id = ? AND dep_type = ?",
            (src, tgt, stored),
        ):
            raise click.ClickException(
                f"No '{dep_type}' relation: {task_id_display(source_id)} → {task_id_display(target_id)}"
            )
        # The Go executor owns the delete and records the 'revisited' touch for
        # both endpoints.
        from endless.event_bridge import emit_event

        _, proj_name = _resolve_project(None)
        emit_event(
            kind="task_dep.deleted",
            project=proj_name,
            entity_type="task_dep",
            entity_id=str(src),
            payload={"source_id": src, "target_id": tgt, "dep_type": stored},
            prompt_verb="unlinked for",
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
    from endless.event_bridge import emit_event

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task_dep.deleted",
        project=proj_name,
        entity_type="task_dep",
        entity_id=str(row["source_id"]),
        payload={
            "source_id": row["source_id"],
            "target_id": row["target_id"],
            "dep_type": row["dep_type"],
        },
        prompt_verb="unlinked for",
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

    if outcome and outcome.strip():
        _mirror_doc_to_worktree(old_id, "outcomes", "outcome", outcome)

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
        rel_label = RELATION_LABELS.get(display_name, display_name)
        rel = rel_label.lower()
        for d in items:
            flat.append(
                {"id": d["id"], "rel": rel, "rel_label": rel_label,
                 "status": d["status"]})
    flat.sort(key=lambda r: r["id"])
    return flat


def _bullet_label_width(labels) -> int:
    """Column width for a '- <Label>:' row in the 'This task:' / 'Touched by:'
    blocks: the longest label plus ':' plus two trailing spaces. 0 for no labels.
    Shared so the two adjacent blocks can align to one column (E-1866)."""
    widths = [len(label) for label in labels]
    if not widths:
        return 0
    return max(widths) + 1 + 2  # ':' + two trailing spaces


def _echo_links_section(
    item_id: int, min_width: int = 0, links: list[dict] | None = None,
) -> bool:
    """Emit the unified multi-line links section (E-1576): a cyan 'This task:' heading,
    then one '- '-bulleted, colored row per relation (id-ascending) — '- <Relation
    phrase>:  E-NNN [status]'. The heading supplies the subject and the leading
    directional phrase (Blocks / Blocked by / Cleans up / Cleaned up by / …) carries the
    full direction, so the row reads as a sentence about the current task. Phrases are
    left-aligned in a fixed column so the ids line up. Rows lead with a '- ' marker (not
    bare indentation) so markdown viewers that collapse leading whitespace (e.g. glow)
    still render them as a list bound to the heading. Titles are intentionally omitted to
    keep every row on one line. Emits nothing and returns False when the task has no
    relations; returns True otherwise. Shared by task show/detail and relations/deps so
    both render identically.

    `min_width` floors the label column so `task show` can align this block with the
    'Touched by:' block that follows it; `links` accepts a pre-computed row set so the
    caller can measure both blocks without querying relations twice (E-1866)."""
    if links is None:
        links = _flatten_relations(item_id)
    if not links:
        return False
    width = max(_bullet_label_width(r["rel_label"] for r in links), min_width)
    click.echo(click.style("This task:", fg="cyan"))
    for r in links:
        color = "green" if r["status"] in _RELATION_TERMINAL_STATUSES else "yellow"
        label = (r["rel_label"] + ":").ljust(width)
        click.echo(
            f"- {label}{task_id_display(r['id'])} "
            f"[{click.style(r['status'], fg=color)}]")
    return True


# Label for a session_tasks row whose relation_id is NULL — a pre-E-1462
# historical touch, recorded before the relation vocabulary existed. The row
# still proves the session touched the task; only the *how* is unknown.
_UNCLASSIFIED_TOUCH_LABEL = "Touched"

# State shown for a touch whose session row is gone. session_tasks deliberately
# carries no FK to sessions so a "session N touched task M" record outlives the
# session (see internal/schema/schema.sql); the touch is still real history.
_MISSING_SESSION_STATE = "gone"

# States that mean the session is no longer in flight, colored green like a
# terminal task status in the relations block. `gone` belongs here too: a touch
# whose session record is absent can't be live. Everything else is in flight.
_FINISHED_SESSION_STATES = ("ended", _MISSING_SESSION_STATE)


def _session_touches(item_id: int) -> list[dict]:
    """Every session that touched this task, most-recent touch first (E-1866).

    One row per session_tasks entry, carrying the session id, how the task
    entered that session's scope (goal / surfaced / revisited, per ED-1497), the
    session's current active task, and its state. Ordered by touch recency
    because the block exists for navigation — the session worth jumping to is
    almost always the one that touched the task last — which is deliberately
    *not* the id-ascending order of the 'This task:' relations block.

    LEFT JOINs throughout: relation_id is NULL for pre-E-1462 rows, and the
    sessions row may be gone entirely (session_tasks has no FK by design).
    """
    rows = db.query(
        "SELECT st.session_id AS session_id, "
        "       st.created_at AS first_touch, "
        "       st.updated_at AS last_touch, "
        "       r.slug        AS rel_slug, "
        "       r.label       AS rel_label, "
        "       s.state       AS state, "
        "       s.active_task_id AS active_task_id "
        "FROM session_tasks st "
        "LEFT JOIN session_task_relations r ON r.id = st.relation_id "
        "LEFT JOIN sessions s ON s.id = st.session_id "
        "WHERE st.task_id = ? "
        "ORDER BY st.updated_at DESC, st.session_id DESC",
        (item_id,),
    )
    return [
        {
            "session_id": row["session_id"],
            "first_touch": row["first_touch"],
            "last_touch": row["last_touch"],
            "rel_slug": row["rel_slug"],
            "rel_label": row["rel_label"] or _UNCLASSIFIED_TOUCH_LABEL,
            "state": row["state"] or _MISSING_SESSION_STATE,
            "active_task_id": row["active_task_id"],
        }
        for row in rows
    ]


def _creating_session(touches: list[dict]) -> dict | None:
    """The touch that created the task, or None (E-1866).

    A task created inside a session gets a `surfaced` session_tasks row (per
    ED-1497 the relation is set once, at capture time, so a later claim or edit
    never overwrites it). Absent for a task filed outside any session and for
    pre-E-1462 rows, whose relation is NULL — in both cases the Created: line
    stays as it was. The earliest touch wins if more than one session ever
    surfaced the task (an import replayed in a second session).
    """
    surfaced = [t for t in touches if t["rel_slug"] == "surfaced"]
    if not surfaced:
        return None
    return min(surfaced, key=lambda t: t["first_touch"] or "")


def _session_ref(touch: dict) -> str:
    """A touch's identity as one token pair: 'ES-1020 (E-1865)' — the session and
    the task it is currently active on, or bare 'ES-1020' when it has none."""
    ref = session_id_display(touch["session_id"])
    if touch["active_task_id"]:
        ref += f" ({task_id_display(touch['active_task_id'])})"
    return ref


def _session_json(touch: dict) -> dict:
    """A touch as a JSON object for `task show --json` (E-1866). Ids render in
    their display form (ES-NNN / E-NNN) like every other id in that payload.
    `relation` is null for a pre-E-1462 row whose relation was never recorded."""
    return {
        "session": session_id_display(touch["session_id"]),
        "relation": touch["rel_slug"],
        "active_task": (
            task_id_display(touch["active_task_id"])
            if touch["active_task_id"] else None
        ),
        "state": touch["state"],
        "touched_at": touch["last_touch"],
    }


def _echo_touched_by_section(touches: list[dict], min_width: int = 0) -> bool:
    """Emit the 'Touched by:' session block (E-1866) — the session-side peer of
    'This task:', laid out identically so the two read as siblings: a cyan
    heading, then one '- '-bulleted row per session, '- <Relation>:  ES-NNN
    (E-NNN) [state]'. The relation carries how the task entered that session's
    scope, the ES-NNN id feeds `session goto` directly, and the parenthesized
    task is what the session is active on now. Emits nothing and returns False
    when no session ever touched the task."""
    if not touches:
        return False
    width = max(_bullet_label_width(t["rel_label"] for t in touches), min_width)
    click.echo(click.style("Touched by:", fg="cyan"))
    for t in touches:
        color = "green" if t["state"] in _FINISHED_SESSION_STATES else "yellow"
        label = (t["rel_label"] + ":").ljust(width)
        click.echo(
            f"- {label}{_session_ref(t)} "
            f"[{click.style(t['state'], fg=color)}]")
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
