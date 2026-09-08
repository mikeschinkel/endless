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
from typing import NamedTuple

import click
from tabulate import tabulate

from endless import agent_help
from endless import db, config
from endless import rowcap
from endless import session_states
from endless import statuses
from endless.statuses import TASK_STATUSES
from endless.project_path import project_name_for_cwd, resolved


_TIER_LABELS = {0: "n/a", 1: "auto", 2: "quick", 3: "deep", 4: "discuss"}
_TIER_FROM_LABEL = {v: k for k, v in _TIER_LABELS.items()}

# Sentinel meaning "tier IS NULL" for filtering
TIER_NONE = -1
# Sentinel meaning "clear tier to NULL" for update
TIER_CLEAR = -2
# Sentinel meaning "parent_id IS NULL" (root tasks only)
PARENT_NONE = 0


# Task relation vocabulary (E-957/E-958; informs dropped per E-1003;
# documents added per E-1007; duplicates added per E-1185).
# display_name -> (stored_dep_type, swap_source_target)
# Stored types are active voice (source is the actor): blocks, implements,
# replaces, duplicates, documents, cleans_up, reverses, modifies, relates_to.
# Inverse views (blocked_by, implemented_by, etc.) resolve to the same stored
# row queried with source/target swapped. The reverses/modifies pair (E-1156)
# are decision-to-decision relations; the others are task-to-task or task-
# to-decision.
CANONICAL_DEP_TYPES: dict[str, tuple[str, bool]] = {
    "blocks":          ("blocks",     False),  # source blocks target
    "blocked_by":      ("blocks",     True),   # inverse view
    "implements":      ("implements", False),  # source implements target
    "implemented_by":  ("implements", True),
    "replaces":        ("replaces",   False),  # source replaces target
    "replaced_by":     ("replaces",   True),
    "duplicates":      ("duplicates", False),  # source is the redundant filing of target's concern; target is the one kept
    "duplicated_by":   ("duplicates", True),
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

# The 9 canonical stored types (the values in CANONICAL_DEP_TYPES, deduplicated).
STORED_DEP_TYPES = (
    "blocks", "implements", "replaces", "duplicates", "documents",
    "cleans_up", "reverses", "modifies", "relates_to",
)

# Display order for `task show` — actionability descending; symmetric last.
# `duplicates` sits with `replaces`: both say "this one is not the task to do,
# that one is", and reading them adjacently is how you tell them apart.
RELATION_DISPLAY_ORDER = (
    "blocked_by", "blocks",
    "implements", "implemented_by",
    "replaces",   "replaced_by",
    "duplicates", "duplicated_by",
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
    "duplicates":     "Duplicates",
    "duplicated_by":  "Duplicated by",
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
            f"Invalid parent '{value}'. Expected 'none' or a task ID (e.g. E-101)"
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
DESCRIPTION_MAX_LENGTH = 1024

# The no-change half of a verdict line (E-2097). A refusal that never says
# whether it partially applied leaves the caller to find out by reading the
# record back — or, as happened, by writing a probe value to a live task.
# The verb that refused picks the precise one; NOTHING_WRITTEN is the default
# because these validators are shared (decisions call the description one), and
# a shared default that says "created" would be wrong half the time.
NOTHING_CREATED = "Nothing was created."
NOTHING_CHANGED = "Nothing was changed."
NOTHING_WRITTEN = "Nothing was written."

# One remedy for the whole long-form family — title over length, description
# over length, description with a newline. All three are the same mistake
# (long-form content in a short field) with the same fix, so they collapse to
# a single clause however many of them fired.
_LONG_FORM_REMEDY = (
    "Long-form goes in --analysis (rationale) or --text (plan); "
    "the title names WHAT and the description is a 2-3 sentence blurb."
)


class _Refusal(NamedTuple):
    """One field problem, held rather than raised, so a call can report all of them.

    `verdict` is the dense fragment for the one-line verdict — measured
    numbers, never an adjective, because the number is what makes the
    correction computable. `remedy` says where the content belongs instead;
    identical remedies dedupe. `guidance` is the full teaching block, unchanged
    from what this refusal has always printed.

    `blank_before` reproduces the blank line the title-length refusal has
    always echoed to stderr ahead of itself. It is suppressed for an agent:
    the point of the bracket is that the verdict is the FIRST line, and a
    leading blank makes it the second.
    """
    verdict: str
    remedy: str
    guidance: str
    blank_before: bool = False


def _title_problems(title: str, force: bool) -> list[_Refusal]:
    """Everything wrong with a title, collected (E-2097).

    Reject titles that don't start with a registered actionable verb. On a
    miss, ask claude haiku whether the first word is a verb; if YES,
    auto-register it (E-1264) and let the title pass. NO / failure falls
    through to the standard refusal.

    Also reject titles longer than TITLE_MAX_LENGTH (E-1517). Length is a
    structural constraint, not a heuristic — `force` does NOT bypass it.

    Add new verbs manually with: endless verb add <new-verb> --definition "<def>"
    """
    if len(title) > TITLE_MAX_LENGTH:
        # Length short-circuits the verb check, as it always has: a title
        # 34 characters over the cap is not being refused for its first word,
        # and the verb check costs a model call to say so.
        return [_Refusal(
            verdict=f"title {len(title)}>{TITLE_MAX_LENGTH} chars",
            remedy=_LONG_FORM_REMEDY,
            blank_before=True,
            guidance=(
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
            ),
        )]

    first_word = title.split()[0].lower() if title.strip() else ""
    from endless import matchers
    verbs = matchers.get_verbs()
    if first_word in verbs or force:
        return []

    # E-1264: ask claude haiku whether the first word is a verb. If YES,
    # auto-register it and let the title pass. This removes the agent's
    # bypass option (rewriting the title with a different verb) that was
    # wasting tokens across sessions. NO / failure paths fall through to
    # the standard refusal below.
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
            return []

    register_cmd = (
        f"endless verb add '{first_word}' --definition \"<short definition>\""
    )

    if agent_help.agent_facing():
        guidance = (
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
        guidance = (
            f"Title must start with an actionable verb. '{first_word}' is not registered.\n"
            f"  Register it (if it really is a verb): {register_cmd}"
        )
    return [_Refusal(
        verdict=f"title's first word '{first_word}' is not a registered verb",
        remedy=(f"Register a real verb with: {register_cmd} — otherwise rewrite "
                f"the title; never register a non-verb to get past this."),
        guidance=guidance,
    )]


def _description_problems(description: str | None) -> list[_Refusal]:
    """Everything wrong with a description, collected (E-2097).

    Reject descriptions longer than DESCRIPTION_MAX_LENGTH or with embedded
    newlines. Per E-1058 / E-1073: description is a 2-3 sentence blurb, not
    long-form. Empty or None is allowed here; required-ness is E-963's concern.

    Both problems are reported together. They used to be sequential raises, so
    a description that was long AND multi-line cost two round trips to learn.
    """
    if not description:
        return []
    problems: list[_Refusal] = []
    if len(description) > DESCRIPTION_MAX_LENGTH:
        problems.append(_Refusal(
            verdict=f"description {len(description)}>{DESCRIPTION_MAX_LENGTH} chars",
            remedy=_LONG_FORM_REMEDY,
            guidance=(
                f"Description is {len(description)} characters; max is {DESCRIPTION_MAX_LENGTH}.\n"
                f"  Description is a 2-3 sentence blurb, not a dissertation. Long-form context\n"
                f"  belongs in a dedicated field: analysis in --analysis, plans/verification in --text."
            ),
        ))
    if "\n" in description or "\r" in description:
        problems.append(_Refusal(
            verdict="description contains a newline; it must be one line",
            remedy=_LONG_FORM_REMEDY,
            guidance=(
                "Description must be a single line; embedded newlines are not allowed.\n"
                "  Description is a brief blurb. Long-form context belongs in a dedicated field:\n"
                "  analysis in --analysis, plans/verification in --text."
            ),
        ))
    return problems


def _refuse(problems: list[_Refusal], action: str) -> None:
    """Refuse once, for every problem found (E-2097). A no-op when there are none.

    The verdict line is assembled here so every refusal that reaches an agent
    has the same shape: sentinel, the command that produced it, the measured
    problems, where the content goes instead, and whether anything changed.
    """
    if not problems:
        return
    verdict = "; ".join(p.verdict for p in problems) + "."
    remedies: list[str] = []
    for problem in problems:
        if problem.remedy and problem.remedy not in remedies:
            remedies.append(problem.remedy)
    summary = " ".join([verdict, *remedies, action])

    # A lone problem renders today's message unchanged, trailing newline and
    # all — a human must see no difference. Only a multi-problem refusal, which
    # could not happen before, normalizes the seam between guidance blocks.
    if len(problems) == 1:
        guidance = problems[0].guidance
    else:
        guidance = "\n\n".join(p.guidance.rstrip("\n") for p in problems)

    if not agent_help.agent_facing() and any(p.blank_before for p in problems):
        click.echo("", err=True)
    raise click.ClickException(agent_help.agent_error(summary, guidance))


def validate_fields(
    title: str | None = None,
    description: str | None = None,
    force: bool = False,
    action: str = NOTHING_WRITTEN,
) -> None:
    """Validate every field of one write, and refuse ONCE with all the problems.

    The write boundary for `task add` and `task update`. Passing None for a
    field means "not being written", which is not the same as writing an empty
    one — `task update` clears a description with the empty string.

    Collecting first is the point (E-2097). Raising on the first failure made a
    title-and-description problem cost two refusals to discover, and each
    refusal read as the only thing wrong.
    """
    problems: list[_Refusal] = []
    if title is not None:
        problems.extend(_title_problems(title, force))
    if description is not None:
        problems.extend(_description_problems(description))
    _refuse(problems, action)


def validate_title(title: str, force: bool = False):
    """Validate a title on its own. See `_title_problems` for the rules."""
    _refuse(_title_problems(title, force), NOTHING_WRITTEN)


def validate_description(description: str | None):
    """Validate a description on its own. See `_description_problems`."""
    _refuse(_description_problems(description), NOTHING_WRITTEN)


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
    """Hierarchical id prefix for a handoff's task line (E-1620).

    A task with a parent renders `E-<parent>/E-<id>`; a root task (standalone
    or epic) renders the bare `E-<id>`. The rule keys solely on parent
    presence, regardless of the parent's type. Introduced to label background
    agents in Agent View; it outlived them (E-2074) because every handoff
    opens with the same line.
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
        "JOIN live_tasks t ON t.project_id = p.id "
        "WHERE t.id = ? LIMIT 1",
        (task_id,),
    )
    if not row:
        return None
    return resolved(row[0]["path"])


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
        name = project_name_for_cwd(cwd)
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
        # Plans on disk spell paths absolutely, so match on the resolved
        # form rather than the `~/...` the column holds (E-2011).
        proj_path = str(resolved(row[0]["path"])) if row else ""

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
            plan_path = resolved(row[0]["path"]) / "PLAN.md"
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
        # E-1927: same relation guard as `task remove`, before the delete.
        _refuse_bulk_clear_with_relations(project_id, source_file)
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

    # E-2064: the Status cell is the BARE status here. The supersession notes
    # (E-1956 `replaced by`, E-1185 `duplicates`) belong to `task show`, which
    # has one status and unlimited width. A table has many rows sharing one
    # column, so the longest cell — 'obsolete (replaced by E-1367)', roughly
    # three times a bare status — is charged to every row's title. A handful of
    # annotated rows cannot cost the whole table its titles for a fact any
    # reader recovers by opening the task.
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
    removed_only: bool = False,
    limit: int | None = None,
    no_limit: bool = False,
):
    """Show tasks for a project as a flat sorted table.

    When type_filter is set (e.g. "epic"), the query joins task_types and
    keeps only rows whose type slug matches — this is the single-source
    listing path shared by `endless epic list`.

    `removed_only` (E-1929) REPLACES the live set rather than adding to it: it
    reads the raw `tasks` table filtered to removed = 1, so removed and live
    rows never interleave in one listing. It also bypasses the default
    terminal-status exclusion — a removed task keeps whatever status it had, and
    "show me what was removed" should not silently drop the obsolete ones. An
    explicit --status still narrows it.

    The row cap (E-2071) is applied AFTER the query, not pushed into it, so the
    footer can name an exact remainder and the confirmed tally below stays a
    tally of the whole set rather than of the rendered page.
    """
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
    project_id, proj_name = _resolve_project(project_name)

    # Reads go through live_tasks so removed rows can never leak into a listing.
    # --removed is the one exception, and says so above.
    table = "tasks" if removed_only else "live_tasks"
    join = ""
    where = "WHERE pi.project_id = ?"
    params: list = [project_id]
    if removed_only:
        where += " AND pi.removed = 1"
        show_all = True
    if type_filter is not None:
        join = " JOIN task_types tt ON tt.id = pi.type_id"
        where += " AND tt.slug = ?"
        params.append(type_filter)
    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND pi.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += f" AND pi.status NOT IN ({statuses.sql_list('terminal')})"
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
        f"FROM {table} pi{join} {where} "
        f"ORDER BY {order_by}",
        tuple(params),
    )

    noun = "removed tasks" if removed_only else "tasks"
    if not rows:
        if as_json:
            click.echo("[]")
        elif llm:
            click.echo(f"# {proj_name}\n(no {noun})")
        else:
            click.echo(
                click.style("•", fg="cyan")
                + f" No {noun} for "
                + click.style(proj_name, bold=True)
            )
        return

    # E-1956: the supersession travels with the status. E-1185: `duplicates` is
    # the second relation with that property. E-2064 narrows "every output mode"
    # to these two: the human table below renders the bare status, so it must
    # not pay for two queries it never reads.

    # Both tallies are taken BEFORE the cap: the summary line reports the whole
    # result set, and the footer says how much of it is off-screen.
    total = len(rows)
    confirmed = sum(1 for r in rows if r["status"] == "confirmed")
    rows, hidden = rowcap.cap_rows(rows, cap)

    _ids = [row["id"] for row in rows]
    replaced = replaced_by_map(_ids) if (as_json or llm) else {}
    duplicated = duplicates_map(_ids) if (as_json or llm) else {}

    if as_json:
        import json
        out = [
            {
                "id": f"E-{row['id']}",
                "phase": row["phase"],
                "status": row["status"],
                # JSON is DATA, not a rendering, so the relation is emitted
                # whenever it exists — the terminal-status gate the human and
                # --llm views apply is a display rule, and a consumer is
                # entitled to the raw fact. Always present (possibly empty) so
                # an absent key never has to be read as "not replaced" /
                # "not a duplicate" (E-1185 adds `duplicates` on the same rule).
                "replaced_by": [
                    f"E-{i}" for i in replaced.get(row["id"], ())
                ],
                "duplicates": [
                    f"E-{i}" for i in duplicated.get(row["id"], ())
                ],
                "tier": row["tier"],
                "title": row["title"],
                "parent": f"E-{row['parent_id']}" if row["parent_id"] else None,
                "created": row["created_at"],
                "confirmed": row["completed_at"] or None,
            }
            for row in rows
        ]
        click.echo(json.dumps(out, indent=2))
        rowcap.echo_footer(hidden, llm=True, err=True)
        return

    if llm:
        click.echo(f"# {proj_name} (removed)" if removed_only else f"# {proj_name}")
        for row in rows:
            tier_val = row["tier"]
            tier_str = f" tier={_TIER_LABELS[tier_val]}" if tier_val else ""
            # key=value rather than the human view's parenthetical, so the line
            # stays parseable — but in the same position, right after the status
            # it qualifies.
            rb_str = (
                " replaced_by=" + ",".join(
                    f"E-{i}" for i in replaced[row["id"]]
                )
            ) if replaced_by_note(row["status"], replaced.get(row["id"])) else ""
            dup_str = (
                " duplicates=" + ",".join(
                    f"E-{i}" for i in duplicated[row["id"]]
                )
            ) if duplicates_note(row["status"], duplicated.get(row["id"])) else ""
            click.echo(
                f"E-{row['id']} {row['phase']} "
                f"{row['status']}{tier_str}{rb_str}{dup_str} {row['title']}"
            )
        rowcap.echo_footer(hidden, llm=True)
        return

    # Header
    click.echo()
    click.echo(
        click.style(
            f"Removed tasks for {proj_name}" if removed_only
            else f"Tasks for {proj_name}",
            bold=True,
        )
    )

    _render_flat_table(rows)
    rowcap.echo_footer(hidden)

    click.echo()
    # The tally counts the WHOLE result set, not the rendered page — with the
    # footer above it, "20 of 1360" is legible; two numbers that both say 20
    # are the silence this task removed.
    click.echo(click.style(
        f"{total} item(s)"
        + (f", {confirmed} confirmed" if confirmed else ""),
        dim=True,
    ))


def next_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    limit: int | None = None,
    no_limit: bool = False,
    llm: bool = False,
    as_json: bool = False,
    tier: int | None = None,
    phase_filter: str | None = None,
    parent_id: int | None = None,
):
    """Show top actionable leaf tasks, ranked by priority."""
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
    # E-1845: `untriaged` is excluded — a task nobody has looked at yet is not
    # actionable work, and offering it here would present it as a ready-to-pick-
    # up item. It surfaces in `session status` (as ◌ triage) instead, which is
    # where the routing decision belongs.
    where = (
        f"WHERE t.status NOT IN ({statuses.sql_list('not-actionable')}) "
        "AND (SELECT count(*) FROM live_tasks c WHERE c.parent_id = t.id) = 0 "
        "AND t.id NOT IN ("
        "  SELECT td.target_id FROM task_deps td"
        "  WHERE td.target_type = 'task' AND td.dep_type = 'blocks'"
        "    AND td.source_id IN ("
        "      SELECT t2.id FROM live_tasks t2 "
        f"      WHERE t2.status NOT IN ({statuses.sql_list('terminal')})"
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

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status, t.tier, p.name as project_name "
        f"FROM live_tasks t "
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
        f"  t.updated_at DESC",
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

    # Capped before grouping: the ordering that ranks these rows is global, so
    # the cap has to bite on the ranked list, not on each project's slice of it.
    rows, hidden = rowcap.cap_rows(rows, cap)

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
        rowcap.echo_footer(hidden, llm=True, err=True)
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
    rowcap.echo_footer(hidden, llm=llm)
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


def _active_status_ranking() -> str:
    """The `WHEN <status> THEN <n>` body ordering the `task active` listing.

    Derived from taskstatus' ordered `active` group (E-1891) so the sort order
    and the WHERE clause above it can never name different statuses — they used
    to be two hand-written lists inside the same query.
    """
    return " ".join(
        f"WHEN '{status}' THEN {rank}"
        for rank, status in enumerate(statuses.get("active"))
    )


def active_tasks(
    project_name: str | None = None,
    show_all: bool = False,
    llm: bool = False,
    as_json: bool = False,
    parent_id: int | None = None,
):
    """Show tasks that are underway or awaiting verification."""
    where = f"WHERE t.status IN ({statuses.sql_list('active')})"
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
        f"FROM live_tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY "
        f"  CASE t.status {_active_status_ranking()} END, "
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
    limit: int | None = None,
    no_limit: bool = False,
    llm: bool = False,
    as_json: bool = False,
    parent_id: int | None = None,
):
    """Show most recently updated tasks."""
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
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

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status, t.tier, p.name as project_name "
        f"FROM live_tasks t "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"ORDER BY t.updated_at DESC",
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

    # Capped before grouping: the ordering that ranks these rows is global, so
    # the cap has to bite on the ranked list, not on each project's slice of it.
    rows, hidden = rowcap.cap_rows(rows, cap)

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
        rowcap.echo_footer(hidden, llm=True, err=True)
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
    rowcap.echo_footer(hidden, llm=llm)
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
    limit: int | None = None,
    no_limit: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """List tasks that have landed at least once, most-recent landing first (E-1478)."""
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
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

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) AS title, "
        f"t.status, t.tier, p.name AS project_name, "
        f"MAX(l.landed_at) AS last_landed, COUNT(l.id) AS land_count "
        f"FROM task_landings l "
        f"JOIN live_tasks t ON t.id = l.task_id "
        f"JOIN projects p ON t.project_id = p.id "
        f"{where} "
        f"GROUP BY t.id "
        f"ORDER BY last_landed DESC",
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

    rows, hidden = rowcap.cap_rows(rows, cap)

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
        rowcap.echo_footer(hidden, llm=True, err=True)
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
    rowcap.echo_footer(hidden, llm=llm)
    if not llm:
        click.echo()


def landed_item(item_id: int, llm: bool = False, as_json: bool = False):
    """Show the full landing history for a single task, newest first (E-1478)."""
    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) AS title, "
        "p.name AS project_name "
        "FROM live_tasks t JOIN projects p ON t.project_id = p.id "
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
#
# "Unlanded" means the branch holds a commit whose CONTENT the base branch
# lacks, not one whose SHA it lacks (E-2087). `worktree land` rebases, so those
# are different questions, and only the first one is the one being asked.


def _unsettled_probe(paths: list[Path]) -> list[dict]:
    """Return the Go unsettled breakdown for each path, in the same order.

    One subprocess for the whole batch: the list view inspects every active
    worktree, and a per-worktree invocation would make it visibly slow.

    Path-based (never --task-id) for E-1766's reason: inside a self-dev worktree
    a DB lookup routes to the per-worktree sandbox, which has no task row.

    The DB context is threaded through for the probe's own bookkeeping (faults),
    not for the verdict: since E-2087 the probe reads no database at all, so
    `--db main` from inside a worktree and a bare run agree by construction.
    """
    if not paths:
        return []
    binary = shutil.which("endless-go")
    if not binary:
        raise click.ClickException("endless-go not found on PATH")
    from endless import config
    result = subprocess.run(
        [binary, *config.go_db_context_args(),
         "session-query", "worktree-unsettled", *(str(p) for p in paths)],
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
            "FROM live_tasks t WHERE t.project_id = ? AND t.id IN "
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
    limit: int | None = None,
    no_limit: bool = False,
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
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
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
    shown, hidden = rowcap.cap_rows(rows, cap)

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
        rowcap.echo_footer(hidden, llm=True, err=True)
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
        rowcap.echo_footer(hidden, llm=True)
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
    rowcap.echo_footer(hidden)
    click.echo()
    click.echo(click.style("  ", fg="cyan") +
               f"Detail for one: endless task unsettled <id>")
    click.echo()


def _unsettled_color(probe: dict) -> str:
    """Colour a reason by which fix it demands: red to investigate, yellow to
    commit, cyan to land."""
    if probe.get("undetermined"):
        return "red"
    if not probe["unsettled"]:
        return "green"
    return "yellow" if probe["modified"] else "cyan"


_PROBE_ERROR_LABELS = (
    ("lookup_error", "worktree lookup"),
    ("base_error", "default-branch resolution"),
    ("status_error", "git status"),
    ("unlanded_error", "the unlanded-commit comparison"),
)


def _probe_errors(probe: dict) -> list[tuple[str, str]]:
    """Return [(label, message)] for each probe that failed."""
    return [(label, probe[key]) for key, label in _PROBE_ERROR_LABELS if probe.get(key)]


def _echo_probe_errors(probe: dict) -> None:
    """Surface failed probes and what the verdict does about them.

    E-1940 flipped the polarity this used to apologise for. A probe that cannot
    run no longer reads as settled; it makes the verdict UNDETERMINED, marks the
    task's own ◆, and records a clearable fault. What is said here is therefore
    what to do about it, not a warning that the answer above may be a lie.
    """
    for label, msg in _probe_errors(probe):
        click.echo()
        click.echo(click.style(
            f"  {label} failed ({msg}). The verdict is undetermined, not "
            f"settled — the ◆ marks this task until the probe can run. "
            f"Recorded as an error: endless errors show", fg="red"))


def unsettled_item(item_id: int, llm: bool = False, as_json: bool = False):
    """Explain exactly why one task's worktree is unsettled (E-1865)."""
    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) AS title, t.status, "
        "p.name AS project_name "
        "FROM live_tasks t JOIN projects p ON t.project_id = p.id WHERE t.id = ?",
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
        "undetermined": False, "undetermined_reason": "",
        "base": "",
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
        # Every probe ran and every one came back clean: a failure would have
        # made this unsettled-because-undetermined and taken the branch below.
        click.echo()
        if not probe["has_worktree"]:
            click.echo(click.style("•", fg="cyan") +
                       " No worktree for this task — nothing to land.")
        else:
            base = probe.get("base") or "the base branch"
            click.echo(click.style("•", fg="green") +
                       f" Settled: working tree clean and every commit is on "
                       f"{base}.")
        click.echo()
        return

    if probe.get("undetermined"):
        # Not a return: `git status` may have succeeded and found modified files
        # before `rev-list` failed, and both halves are worth showing. The
        # per-probe remedy is printed by _echo_probe_errors at the end.
        click.echo()
        click.echo(click.style(
            f"  Undetermined — {probe['undetermined_reason']}. "
            f"Endless cannot say whether this worktree holds unlanded work, so "
            f"it is marked rather than reported clean.", bold=True))

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
        base = probe.get("base") or "the base branch"
        click.echo(click.style(
            f"  Unlanded — {probe['unlanded_count']} commit(s) not on {base}. "
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
        "FROM live_tasks t LEFT JOIN task_types tt ON tt.id = t.type_id "
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
    validate_fields(title=title, description=description, force=force,
                    action=NOTHING_CREATED)
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

    # E-1658: gate the title's lead-verb category against the type's accepts
    # (epic exempt). Runs after validate_title so the verb is registered/known.
    _require_verb_category_for_type(title, task_type)

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


# ── E-1889: file-time hints ─────────────────────────────────────────────────
#
# Three advisories printed after a `task add` succeeds. Every one is a HINT and
# never a refusal: sometimes a genuinely separate task IS the right call, and
# telling the two apart needs judgment the CLI does not have. Their job is to
# put the relevant fact in front of whoever is filing at the moment the choice
# is made — not to make the choice for them.
#
# They run after the row exists and are collectively best-effort, so a hint's
# own query can never cost the caller the add it just made.

# How recently the current session must have landed a task for the reopen hint
# to fire. A day out, "you broke what you just shipped" is still the likeliest
# reading of a `--cleans-up` pointed at it; much beyond that it is not.
_HINT_LANDED_WINDOW_HOURS = 24


def _hint_recently_landed(
    cleans_up_ids: tuple[int, ...], session_id: int | None,
) -> list[str]:
    """`--cleans-up E-X` where THIS session landed E-X inside the window.

    That shape is the reflex this task exists to interrupt: a bug in work the
    session just landed is that task done wrong, not a peer beside it. Scoped
    to the session's own landings on purpose — another session's landed work
    is not something this session can claim to have broken.
    """
    if not cleans_up_ids or session_id is None:
        return []
    lines: list[str] = []
    for tid in cleans_up_ids:
        rows = db.query(
            "SELECT CAST((julianday('now') - julianday(landed_at)) * 24 AS INTEGER) "
            "AS hours_ago FROM task_landings "
            "WHERE task_id = ? AND session_id = ? "
            "AND julianday('now') - julianday(landed_at) <= ? "
            "ORDER BY landed_at DESC LIMIT 1",
            (tid, session_id, _HINT_LANDED_WINDOW_HOURS / 24.0),
        )
        if not rows:
            continue
        hours = rows[0]["hours_ago"] or 0
        lines.append(
            f"{task_id_display(tid)} was landed by this session {hours}h ago — "
            f"consider `endless task update {task_id_display(tid)} --status "
            f"revisit` and fixing it there."
        )
    return lines


def _hint_backlog_pressure(item_id: int) -> list[str]:
    """How many open tasks the project now carries.

    A filed task is a standing claim on attention: it is read, re-read, and
    triaged past on every pass, worked or not. That cost is invisible at the
    moment of filing unless something states it, so state it.
    """
    row = db.query(
        "SELECT t.project_id AS pid, p.name AS name FROM live_tasks t "
        "JOIN projects p ON p.id = t.project_id WHERE t.id = ?",
        (item_id,),
    )
    if not row:
        return []
    # "Open" here means pre-judgment: nobody has decided what these are yet,
    # which is exactly what makes them a standing cost on every pass.
    count = db.scalar(
        "SELECT count(*) FROM live_tasks WHERE project_id = ? "
        f"AND status IN ({statuses.sql_list('pre-judgment')})",
        (row[0]["pid"],),
    ) or 0
    return [
        f"{row[0]['name']} now carries {count} open task"
        f"{'' if count == 1 else 's'} "
        f"({'/'.join(statuses.get('pre-judgment'))}). Each one is read "
        f"and triaged past on every pass through the backlog."
    ]


def _hint_same_session_root_cause(
    item_id: int, session_id: int | None,
) -> list[str]:
    """Other tasks this session has already filed, and the root-cause question.

    This is the check that would have caught E-1888/E-1891 — two tasks filed
    in one session that turned out to be a single defect. Deliberately NOT the
    same check as backlog-wide semantic duplication (E-1739): those two were
    different files, different symptoms, not semantically similar. Same-session
    *common cause* and backlog-wide *similarity* are independent questions.
    """
    if session_id is None:
        return []
    rows = db.query(
        "SELECT st.task_id AS id, COALESCE(t.title, t.description) AS title "
        "FROM session_tasks st "
        "JOIN session_task_relations r ON r.id = st.relation_id "
        "JOIN live_tasks t ON t.id = st.task_id "
        "WHERE st.session_id = ? AND r.slug = 'surfaced' AND st.task_id != ? "
        "ORDER BY st.task_id",
        (session_id, item_id),
    )
    if not rows:
        return []
    lines = [
        f"This session has already filed {len(rows)} task"
        f"{'' if len(rows) == 1 else 's'}:"
    ]
    lines += [
        f"  {task_id_display(r['id'])}  {r['title'] or ''}".rstrip()
        for r in rows
    ]
    lines.append(
        "Does the one you just filed share a root cause with any of them? "
        "File the cause, not each symptom."
    )
    return lines


def print_add_hints(item_id: int, cleans_up_ids: tuple[int, ...] = ()) -> None:
    """Print the post-`task add` advisories (E-1889).

    Called by the CLI after the task and its relations exist. Never raises:
    the add has already succeeded, and no advisory is worth failing it for.
    """
    try:
        session_id = _current_endless_session_id()
        # Each recently-landed line stands alone (one per --cleans-up target);
        # the other two hints are one block each.
        blocks = [[ln] for ln in _hint_recently_landed(cleans_up_ids, session_id)]
        blocks.append(_hint_backlog_pressure(item_id))
        blocks.append(_hint_same_session_root_cause(item_id, session_id))
    except Exception:
        return
    blocks = [b for b in blocks if b]
    if not blocks:
        return
    bullet = click.style("▸", fg="yellow")
    click.echo("")
    for block in blocks:
        click.echo(f"{bullet} {block[0]}")
        for extra in block[1:]:
            click.echo(f"  {extra}")


def import_json(
    data: list[dict],
    project_name: str | None = None,
    clear: bool = False,
):
    """Import task items from a JSON array."""
    from endless.event_bridge import emit_event

    project_id, proj_name = _resolve_project(project_name)

    if clear:
        # E-1927: same relation guard as `task remove`, before the delete.
        _refuse_bulk_clear_with_relations(project_id, "json_import")
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
    """Every task id a `task remove` would remove.

    Without `--cascade` that is the one task; with it, the task plus its full
    descendant set. The guard below has to check all of them: if only the root
    were checked, removing a parent would remove a child *and* orphan the
    child's relations, bypassing the guard entirely.

    Reads live_tasks (E-1929), matching the Go executor's own enumeration: an
    already-removed descendant is not re-removed, and the walk covers exactly
    the rows that would have existed back when removal was a hard delete.
    """
    if not cascade:
        return [item_id]
    rows = db.query(
        "WITH RECURSIVE tree(id) AS ("
        "  SELECT id FROM live_tasks WHERE id = ?"
        "  UNION ALL"
        "  SELECT t.id FROM live_tasks t JOIN tree ON t.parent_id = tree.id"
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
    so a task removal leaves both behind.

    E-1929 closed the second half of that exposure (ids are no longer re-minted,
    so a left-behind row can never reattach to unrelated work), but this guard
    still stands on its own: a relation that survives its task is a dangling
    reference whether or not something else can inherit it.
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


def _orphan_refusal(
    headline: str,
    found: list[tuple[int, str]],
    group_by_task: bool,
) -> click.ClickException:
    """Build the refusal every relation-orphaning delete path raises.

    One message shape wherever a task is about to be deleted, so the rule reads
    the same however the caller got here. `group_by_task` attributes each row to
    the id it hangs off — needed whenever more than one task is being deleted,
    or the operator cannot tell which one is holding up the removal.
    """
    lines = [
        headline,
        "Removing would orphan them — relation rows survive a task delete, and",
        "task ids are reused, so a later task inheriting one of these ids would",
        "inherit its relations too. Unlink them first:",
        "",
    ]
    if group_by_task:
        for owner in sorted({owner for owner, _ in found}):
            lines.append(f"  {task_id_display(owner)}:")
            lines += [f"      {cmd}" for o, cmd in found if o == owner]
    else:
        lines += [f"    {cmd}" for _, cmd in found]
    return click.ClickException("\n".join(lines))


def _refuse_removal_with_relations(
    item_id: int, cascade: bool, ids: list[int],
) -> None:
    """Refuse to remove a task while any relation still references it (E-1915).

    Deny rather than cascade: a severed relation cannot be reconstructed, and a
    refusal costs one `unlink`. All relation types, no per-type exemption — one
    rule that always holds beats two rules with a judgment call at the boundary,
    and every "harmless" exemption is a second code path that can orphan rows
    again.

    Must run BEFORE the task.deleted event is emitted, or a refusal would
    publish a removal that never happened.
    """
    found = _relations_referencing(ids)
    if not found:
        return

    subject = task_id_display(item_id)
    raise _orphan_refusal(
        f"{subject} has {len(found)} relation(s)."
        if not cascade else
        f"{subject} and its descendants have {len(found)} relation(s).",
        found,
        group_by_task=cascade,
    )


def _refuse_bulk_clear_with_relations(project_id: int, source_file: str) -> None:
    """Refuse a bulk clear while relations reference any task it would remove.

    E-1927: `task import --replace` and `task import-json --clear` remove tasks
    by emitting task.bulk_cleared, which the Go executor runs as a bulk removal
    of every task that project's source file owns. That never passes through
    `remove_item`, so before this the two import verbs orphaned relation rows
    exactly as `task remove` did before E-1915.

    Same rule, same message: a severed relation is unrecoverable however the
    task went away, and an imported task that has since been linked to is no
    longer disposable just because a file regenerated it. Clear the relation
    first, or re-import without `--replace` / `--clear`.

    Must run BEFORE task.bulk_cleared is emitted.
    """
    ids = [
        r["id"] for r in db.query(
            "SELECT id FROM live_tasks WHERE project_id = ? AND source_file = ?",
            (project_id, source_file),
        )
    ]
    found = _relations_referencing(ids)
    if not found:
        return

    name = Path(source_file).name if source_file != "json_import" else source_file
    raise _orphan_refusal(
        f"{len(found)} relation(s) reference tasks imported from {name}.",
        found,
        group_by_task=True,
    )


def remove_item(item_id: int, cascade: bool = False):
    """Remove a task.

    E-1929: removal marks the row `removed = 1` rather than deleting it, so the
    id is never re-minted and the FK-free rows that outlive a task can never
    reattach to unrelated work. The guards and messaging here are unchanged —
    only what the emitted event does to the row. The task disappears from every
    listing; `task show E-NNN` still renders it, marked removed, and
    `task list --removed` lists them.
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title FROM live_tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    child_count = db.scalar(
        "SELECT count(*) FROM live_tasks WHERE parent_id = ?",
        (item_id,),
    ) or 0

    if child_count > 0 and not cascade:
        raise click.ClickException(
            f"Task {task_id_display(item_id)} has {child_count} child(ren). "
            f"Use --cascade to remove it and all descendants."
        )

    # The ids this removal covers: the task, plus its descendants under
    # --cascade. Computed ONCE, here, before anything is removed — both the
    # relation guard and the descendant count reported below read it.
    removal_ids = _removal_id_set(item_id, cascade)

    # E-1915: refuse while relations still point at any id being removed. There
    # is deliberately no flag to remove a task and its relations in one step —
    # `--cascade` above is about CHILDREN and predates this.
    _refuse_removal_with_relations(item_id, cascade, removal_ids)

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
        # E-1928: read off removal_ids, captured before the emit above. This
        # used to run its own recursive query HERE — after emit_event, which
        # executes the removal synchronously — so the CTE always seeded from an
        # already-cleared set and every cascade reported "0 descendant(s)".
        desc_count = len(removal_ids) - 1
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
        "SELECT MAX(sort_order) FROM live_tasks "
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


# E-1658: per-task-type accepted verb categories. Validity(verb, type) holds
# when the type's accepted set intersects the lead verb's category set. Types
# ABSENT from this map are exempt from the creation gate — `epic` is a container
# whose title is never verb-gated (preserves the ED-1511 exemption). Decouples
# verbs from types: a new verb touches only its own `category`, a new type only
# this map — O(verbs)+O(types), not the O(verbs×types) a per-pair table would be.
_TYPE_ACCEPTS: dict[str, frozenset[str]] = {
    "todo":       frozenset({"action"}),
    "bugfix":     frozenset({"action"}),
    "research":   frozenset({"investigation"}),
    "brainstorm": frozenset({"investigation"}),
}


def _require_verb_category_for_type(title: str | None, task_type: str | None):
    """E-1658 creation gate: a task's title lead verb must carry a category the
    task's `--type` accepts. `epic` (and any type absent from `_TYPE_ACCEPTS`) is
    exempt. Unregistered or `--force`-passed verbs default to the 'action'
    category (see `matchers.verb_categories`).

    On mismatch the error names the verb, its category, and the type's accepted
    categories. There is deliberately no bypass flag: the fix is a different
    `--type` or a title led by a verb the type accepts, and a genuinely dual verb
    should carry both categories rather than an escape hatch (bypasses get
    taken)."""
    accepts = _TYPE_ACCEPTS.get(task_type or "")
    if accepts is None:
        return
    from endless.matchers import verb_categories
    verb = _lead_verb(title)
    cats = verb_categories(verb)
    if cats & accepts:
        return
    accepts_str = "/".join(sorted(accepts))
    cats_str = "/".join(sorted(cats))
    raise click.ClickException(
        f"Title verb '{verb}' is an {cats_str} verb, but a {task_type!r} task "
        f"accepts only {accepts_str} verbs.\n"
        f"  Lead the title with an {accepts_str} verb, or set --type to one that "
        f"accepts {cats_str} work "
        f"(investigation → research/brainstorm; action → todo/bugfix)."
    )


# E-1658: `completed` eligibility is a TYPE rule, not a verb one. Implementation
# types (todo/bugfix) have no findings deliverable and terminate via the
# verification lane (unverified → confirmed/assumed); research/brainstorm reach
# `completed` via the review lane (unreviewed → completed); epic self-completes.
# The Go transition table (internal/taskstatus/transitions.go) is the source that
# refuses todo/bugfix → completed. The former verb-gate
# (_require_investigation_verb_for_completed, E-1240) is gone: gating a status on
# the title verb put a nice-to-have (verb sensibility) in charge of a
# non-negotiable invariant (which statuses a type may hold).


# Types whose deliverable IS the outcome text, so completing one requires
# --outcome. ED-1520: the requirement is keyed on task TYPE, not the 'completed'
# status (E-1240 had coupled it to status as a proxy for these types). Epics are
# excluded — they self-complete via child-status derivation, with no interactive
# completion step where an outcome could be supplied. Other types reaching
# 'completed' via an investigation verb are not forced to carry an outcome.
_OUTCOME_REQUIRED_TYPES = ("research", "brainstorm")


# E-2016 put `unreviewed` in front of `completed` for these same two types, and
# `unreviewed` means "outcome written, awaiting the owner's read". That is the
# step where the deliverable now actually arrives, so it is where the
# requirement has to bite: an `unreviewed` task with no outcome hands the owner
# an empty gate to sign off on, which is a worse failure than the one E-2016
# set out to fix. `completed` keeps the check too — a task can still arrive
# there directly on a type not routed through the gate.
_OUTCOME_REQUIRED_STATUSES = ("completed", "unreviewed")


def _require_outcome_for_completed(
    status: str | None,
    task_type: str | None,
    outcome: str | None,
):
    """ED-1520: completing a research/brainstorm task requires --outcome — the
    outcome IS the deliverable for those types. Keyed on type, not the
    'completed' status. Decline's own reason requirement is separate
    (`_require_outcome_for_declined`, ED-1022).

    E-2016: also required at `unreviewed`, which is where the outcome is
    written for those types."""
    if (status in _OUTCOME_REQUIRED_STATUSES
            and (task_type or "") in _OUTCOME_REQUIRED_TYPES
            and not (outcome and outcome.strip())):
        verb = "completing" if status == "completed" else "submitting"
        raise click.ClickException(
            f"An outcome is required when {verb} a {task_type} task — "
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
# E-1891: the type→policy mapping stays here (it is about types, not statuses);
# only the status set moves out, as taskstatus' `verification-track` group.
# E-2016 adds the inverse half. The gate above only ever pointed one way —
# findings types refused the verification lane — which left the review lane
# open to everyone. `unreviewed` means "the outcome is written, awaiting the
# owner's read", and that is not a state implementation work has: a todo or a
# bugfix is gated by `unverified`, and routing it through a second gate would
# say the two lanes are one. Both directions are now refused, so the tracks are
# fully separated rather than half.
# The FINDINGS lane's statuses — its gate (`unreviewed`) AND its terminal
# (`completed`). The Go review-track group carries only the gate, so `completed`
# is added here to complete the lane, symmetric to verification-track (which
# already carries its gate `unverified` plus its terminals confirmed/assumed).
# E-1658: this is the type rule that refuses `todo`/`bugfix` → `completed` on the
# `task complete` path (mark_completed_item), which bypasses the Go transition
# table — transitions.go governs only `task update --status`.
_FINDINGS_LANE = (*statuses.get("review-track"), "completed")

_TYPE_FORBIDDEN_STATUSES = {
    "research":   statuses.get("verification-track"),
    "epic":       statuses.get("verification-track"),
    # E-1657/ED-1516: brainstorm's deliverable is the synthesis (information,
    # not testable behavior), so like research it terminates via 'completed
    # --outcome' and never goes through user-testable verification.
    "brainstorm": statuses.get("verification-track"),
    # E-1658: implementation types finish via the verification lane and are
    # refused the whole findings lane, `completed` included.
    "todo":       _FINDINGS_LANE,
    "bugfix":     _FINDINGS_LANE,
}


def _require_status_allowed_for_type(status: str | None, task_type: str | None):
    """E-1577/E-1579/E-2016: reject type-inappropriate statuses up front.

    Two directions, one table. research/epic/brainstorm reject
    'unverified'/'assumed'/'confirmed' — they terminate via 'completed' (per
    E-1537 §3) and never go through verification. todo/bugfix reject the whole
    findings lane — both 'unreviewed' and 'completed' (E-1658) — because their
    deliverable is testable behavior, gated by 'unverified'.

    This is a type-correctness invariant, not a soft policy: the fix for a
    rejected flip is to change the task type, not to override the gate (so
    there is no --force bypass, matching E-1577's hard gate)."""
    forbidden = _TYPE_FORBIDDEN_STATUSES.get(task_type or "")
    if not forbidden or status not in forbidden:
        return
    # Both refusals name the remedy, and the remedy differs by direction: a
    # findings type is being pushed into the verification lane and belongs at
    # 'completed'; an implementation type is being pushed into the findings lane
    # (its gate 'unreviewed' or its terminal 'completed') and belongs at
    # 'unverified'. Telling either one to "use --status completed" would be wrong
    # half the time.
    if status in _FINDINGS_LANE:
        raise click.ClickException(
            f"Task type {task_type!r} cannot be set to status {status!r}. "
            f"{status!r} is for research and brainstorm work, whose deliverable "
            f"is an outcome someone has to read; {task_type} tasks are gated by "
            f"'unverified' instead. "
            f"Use --status unverified, or change the task type."
        )
    raise click.ClickException(
        f"Task type {task_type!r} cannot be set to status {status!r}. "
        f"{task_type} tasks terminate via 'completed' (with --outcome) and "
        f"never use {'/'.join(repr(s) for s in forbidden)}. "
        f"Use --status completed, or change the task type."
    )


# E-1956: the statuses that mean the task's work SHIPPED — it reached the
# verification gate or passed it. `obsolete` is refused on these.
#
# Deliberately NOT the terminal set (_TERMINAL_STATUSES): `declined`
# and `obsolete` are terminal but never shipped, and 'unverified' ships without
# being terminal. And deliberately CURRENT status only, not "ever reached" — a
# task that shipped and was later reopened to `revisit` is genuinely back in
# play, and re-closing it as obsolete is a legitimate call.
_SHIPPED_STATUSES = statuses.get("shipped")


def _refuse_obsolete_on_shipped_work(
    item_id: int,
    status: str | None,
    current_status: str,
    via_replace: bool = False,
):
    """E-1956: refuse `obsolete` on a task whose work already shipped.

    `obsolete` means "made irrelevant by other changes" — it reads as *never
    happened*, which is simply false of work that ran, merged, and is being
    superseded. The fact worth keeping is the supersession, and that is a
    `replaced_by` relation, not a status. So the tempting-but-lossy move is
    closed off and the caller is pointed at `task replace`, which records the
    relation and leaves the shipped status standing.

    A hard gate with no --force, matching `_require_status_allowed_for_type`
    (E-1577): the fix is to record the right fact, not to override the check.
    `via_replace` only swaps the remedy sentence — `task replace` is already
    the command in hand there, so telling the caller to run it would be noise.
    """
    if status != "obsolete" or current_status not in _SHIPPED_STATUSES:
        return
    if via_replace:
        remedy = (
            f"Omit --status to keep {current_status!r} (the replaced_by "
            f"relation is recorded either way), or name a terminal that is "
            f"true of it."
        )
    else:
        remedy = (
            "If it was superseded, record that instead:\n"
            f"    endless task replace {task_id_display(item_id)} --by <new-id>\n"
            f"(keeps {current_status!r}, adds a replaced_by relation)"
        )
    raise click.ClickException(
        f"{task_id_display(item_id)} is {current_status!r} — shipped work "
        f"cannot be marked obsolete; that reads as \"never happened\" and "
        f"loses the fact that it shipped.\n\n{remedy}"
    )


def _refuse_cascade_across_typed_descendants(item_id: int, status: str):
    """E-1577: when --cascade would set 'assumed'/'confirmed' on a subtree,
    refuse loudly if any descendant is research/epic. Naming offenders
    matches the 'loud failure on invalid state' rule."""
    if status not in statuses.get("verification-terminal"):
        return
    offenders = db.query(
        "WITH RECURSIVE tree(id) AS ("
        "  SELECT id FROM live_tasks WHERE id = ?"
        "  UNION ALL"
        "  SELECT t.id FROM live_tasks t JOIN tree ON t.parent_id = tree.id"
        ") "
        "SELECT t.id, COALESCE(t.title, t.description) AS title, "
        "       COALESCE(tt.slug, '') AS type "
        "FROM live_tasks t "
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
    agent harness or a human explicitly asked to preview the agent's view with
    the global `--agent-view` flag — `agent_help.agent_facing`, the same gate
    the agent `--help` augmentation and the bracketed refusals use.

    And only in a project that actually runs the report channel (E-1966). The
    nudge asserts that all further reporting goes through `task report`; where
    `report_gate` is off nothing routes it and nothing enforces it, so the
    assertion is simply false — the Python twin of the PostToolUse defect
    E-1953 fixed on the Go side. An instruction may outlive its enforcement
    harmlessly; a claim about enforcement may not, because a session told it is
    being checked when it is not learns that Endless's statements about its own
    behavior cannot be relied on."""
    if not agent_help.agent_facing():
        return
    if not _is_report_wind_down(old_status, new_status, outcome_present):
        return
    if not _report_gate_on():
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
        + click.style(
            f"endless task report {task_id_display(item_id)} --draft-file <path>",
            bold=True)
    )
    click.echo(
        "  Write the reply you were about to send to a file, then send that"
    )
    click.echo("  command's output verbatim as your entire message.")


def complete_item(item_id: int, cascade: bool = False, outcome: str | None = None):
    """Mark a task as confirmed."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT t.id, COALESCE(t.title, t.description) as title, t.status, "
        "       COALESCE(tt.slug, '') AS type "
        "FROM live_tasks t "
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
            "  SELECT id FROM live_tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM live_tasks t JOIN tree ON t.parent_id = tree.id"
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
        "FROM live_tasks t "
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
            "  SELECT id FROM live_tasks WHERE id = ?"
            "  UNION ALL"
            "  SELECT t.id FROM live_tasks t JOIN tree ON t.parent_id = tree.id"
            ") SELECT count(*) FROM tree",
            (item_id,),
        ) or 1
        suffix = f"(cascaded to {count - 1} descendant(s))"
    _emit_field_changes(item_id, row[0]["title"], changes, suffix=suffix)
    _maybe_emit_report_reminder(
        item_id, row[0]["status"], "assumed", bool(outcome and outcome.strip())
    )


def mark_completed_item(item_id: int, outcome: str):
    """Mark a findings-as-deliverable task as `completed`.

    Gated by `--outcome` (required) and by the type rule (E-1658):
    `completed` is a findings-lane terminal, so only research/brainstorm/epic
    reach it — implementation types (todo/bugfix) are refused here and finish via
    `confirmed`/`assumed`. This path (`task complete`) bypasses the Go transition
    table, so the type gate is enforced explicitly below. Distinct from
    `confirmed` (behavior verified) and `assumed` (behavior believed correct,
    awaiting promotion)."""
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status, "
        "       COALESCE((SELECT slug FROM task_types WHERE id = live_tasks.type_id), '') AS type "
        "FROM live_tasks WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    _require_outcome_for_completed("completed", row[0]["type"], outcome)
    # E-1658: `task complete` bypasses the Go transition table, so enforce the
    # type→status rule here — todo/bugfix have no findings deliverable.
    _require_status_allowed_for_type("completed", row[0]["type"])

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
        "SELECT id, COALESCE(title, description) as title, status FROM live_tasks "
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


# Statuses a task may be `submit`ted from: pre-approval design states.
#
# E-1845: `untriaged` is included so the new default status is not a dead end.
# E-1859 made the routing automatic, and `task submit` stays exactly as
# important: it is the permanent human override for a triage call you disagree
# with, and the route that still works when the triager is unreachable (it
# fails open, leaving the task here). Never scaffolding to remove.
_SUBMITTABLE_FROM = statuses.get("submittable-from")


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
        "SELECT id, COALESCE(title, description) as title, status FROM live_tasks "
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

    Approval being a human act stays a CONVENTION, not an enforced gate. It was
    enforced against `kind=background` sessions only, and E-2074 removed that
    kind along with background agents — leaving nothing the system can tell
    apart, since it cannot distinguish a human from an agent in a tmux pane.
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM live_tasks "
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


def _in_claude_session() -> bool:
    """Is the process running this command the Claude session being bound?

    The one distinction `task claim`'s outcome turns on (E-2106). A Claude
    session running `endless task claim` through its Bash tool is already here;
    all it needs is its working directory moved, and starting a SECOND Claude
    for a task the first one is about to work would be absurd. Every other
    caller is a shell, and a shell cannot become the session — one has to be
    launched.

    Two ways to be that session, and both mean "this process", not "this
    process can name a session":

      - `CLAUDECODE=1`, which Claude Code sets in the environment of the
        commands it runs (the E-1455 env-vars-as-truth path).
      - `TMUX_PANE` is itself a live Claude pane — a subprocess deeper down the
        same pane, whose env may have been stripped along the way.

    Deliberately NOT included: `ENDLESS_SESSION_ID`. `esu` exports it into a
    plain SHELL, pointing at a Claude session in some other pane or window, so
    reading it as "I am that session" would hand a shell a `/cd` line it cannot
    run.
    """
    if os.environ.get("CLAUDECODE") == "1":
        return True
    pane = os.environ.get("TMUX_PANE")
    if not pane:
        return False
    from endless.session_cmd import _live_sessions, _project_root_for_cwd
    try:
        live = _live_sessions(_project_root_for_cwd())
    except Exception:
        return False
    return any(c.get("pane_id") == pane for c in live)


def _require_tmux_for_claim(item_id: int) -> None:
    """Refuse a shell claim with nowhere to start a Claude session (E-2106).

    A shell cannot become the session, so claiming from one means starting
    Claude, and Claude is delivered in a tmux window. Without tmux there is
    nowhere to put it.

    That is the whole gate now. It used to also refuse a window holding more
    than one pane, because the launch exec'd over the pane the claim was typed
    in and then split the window around it — so it needed the window to itself,
    and destroyed the caller's shell when it got it. Opening a window of its
    own removes both, and with them the refusal: splitting a window nothing
    else is in disturbs nothing.

    Runs BEFORE the claim, so a refusal leaves no half-claimed task behind.

    Reached only when NO session resolved, so it never touches the case where a
    live Claude session in a sibling pane picks the task up (E-1242): that
    claim binds an existing session rather than starting one.
    """
    if os.environ.get("TMUX"):
        return
    raise click.ClickException(
        "No Claude session to bind this task to, and no tmux to start one "
        "in.\n"
        "  Claiming from a shell starts Claude on the task, which needs a tmux "
        "window.\n"
        "  Start tmux and claim again, or claim with no session at all, to "
        "work it by hand:\n"
        f"      endless task claim E-{item_id} --unattended"
    )


def _launch_claude_for_claim(
    item_id: int, project_id: int, worktree: str,
) -> None:
    """Start a Claude session on a just-claimed task, from a shell (E-2106).

    `task claim` used to end by printing a menu, and both of its options were
    wrong: option 1 was `task spawn`, which `_check_prior_claim` refuses on any
    task that has ever been claimed — which a re-claim always has — and option 2
    spelled out `/cd`, `shell-init` and `eswt`, the last of which
    `endless shell-init` has never defined. So the common outcome was a claimed
    task, a built worktree, and no route into it that worked.

    A shell cannot become the session, so this starts one — through the same
    `spawn-window` seam `task spawn` uses, which is what keeps it from being a
    second, parallel way to launch Claude. What it does NOT do is deliver
    spawn's handoff: that is the part that re-reads a task as if it were new,
    and picking work back UP is this task's whole subject. The launcher spells
    "a bare interactive claude" as an empty handoff file, so an empty one is
    what it gets.

    In a window of its own, so the shell the claim was typed in survives.
    This first took over the caller's pane instead — exec'ing Claude over the
    shell and splitting the window around it — which cost the user their shell
    and forced a refusal for any window holding another pane. Both were
    consequences of the exec, not requirements of the claim.

    The session→task binding is not written here, and cannot be: the session
    does not exist yet. `spawn-window` publishes `@endless_spawned_by` and
    `@endless_task_id` on the new window, and SessionStart's spawn-bind reads
    them once Claude is up — the same deferred bind every spawned session gets.
    """
    import tempfile

    from endless import event_bridge

    # Resolved to an absolute path: the Go launcher execs it via syscall.Exec,
    # which does no PATH lookup, and `_claude_binary()` may return a bare name.
    claude = shutil.which(_claude_binary()) or _claude_binary()

    handoff = tempfile.NamedTemporaryFile(
        mode="w", suffix=".md", prefix="endless-claim-", delete=False,
    )
    handoff.close()
    window_name = tmux_window_name(item_id)
    subprocess.run([
        event_bridge._resolve_endless_go(), "spawn-window",
        "--claude-bin", claude,
        "--handoff-file", handoff.name,
        "--permission-mode", "auto",
        "--task-id", str(item_id),
        "--project-id", str(project_id),
        # Spawn's own fallback for a spawner that is not a Claude session: a
        # non-empty marker is what makes SessionStart take the spawn-bind path
        # rather than the cwd fallback.
        "--spawned-by", str(_current_endless_session_id() or f"pid-{os.getpid()}"),
        "--window-name", window_name,
        "--cwd", worktree,
    ], check=True)
    click.echo("")
    click.echo(
        click.style("•", fg="cyan")
        + f" Started Claude on {task_id_display(item_id)} in window "
        + click.style(f"'{window_name}'", bold=True)
    )
    click.echo(f"  Switch to it: tmux select-window -t {window_name}")


def _current_session_task_id() -> int | None:
    """The active task id of the current Endless session, if any.

    Fills the spawn handoff's "Spawning session: E-NNNN" origin line (E-1469).
    Returns None when there is no resolvable current session or it has no
    active task.
    """
    eid = _current_endless_session_id()
    if eid is None:
        return None
    rows = db.query(
        "SELECT task_id FROM sessions WHERE id = ?",
        (eid,),
    )
    if not rows or rows[0]["task_id"] is None:
        return None
    return rows[0]["task_id"]


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
    # E-1807's ghost owner (a session that died without firing SessionEnd,
    # leaving a non-ended row on a now-dead pane) is handled by `_live_sessions`
    # below, which as of E-1898 omits any session whose pane was observably
    # absent. No reaper runs first: nothing is written to make the task free,
    # the ghost simply is not reported as live. A session whose tmux server
    # could not be reached is reported as `unknown` and DOES still hold the
    # task — unprovable is not the same as gone.
    from endless.session_cmd import _live_sessions, _project_root_for_cwd
    project_root = _project_root_for_cwd()

    rows = db.query(
        "SELECT id AS eid FROM sessions "
        f"WHERE task_id = ? AND state IN ({session_states.sql_list('live')})",
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
            "Switch to that session or have it release the task first."
        )

    return owned_by_current


# E-1891: `settled` — the work is over one way or another, shipped or
# abandoned. The same group gates the tier clear in the Go executor.
#
# E-2093 renamed this from `_CLAIM_REQUIRES_FORCE`. It no longer names a flag,
# because no flag clears it any more: re-claiming settled work goes through an
# explicit status transition, and `--force` is deprecated rather than being the
# answer. See `_settled_reopen_route`.
_CLAIM_REFUSED_STATUSES: frozenset[str] = frozenset(statuses.get("settled"))


def _settled_reopen_route(item_id: int, current_status: str) -> str:
    """The `task update --status` call that reopens a settled task, as typed.

    E-2093 removed `--force`'s settled-status demotion from `claim` and
    `spawn`. What replaces it is not another flag: it is saying what you are
    doing. `task update --status <s>` leaves an auditable transition on the
    task; a demotion riding a flag on an unrelated verb leaves nothing.

    The status has to be one the task can actually REACH, because a refusal
    that names a command the next call rejects is the defect this task exists
    to remove. Two routes, split on the `shipped` group:

      - shipped work (`unverified`/`unreviewed`/`confirmed`/`assumed`/
        `completed`) reopens to `revisit` — "needs re-evaluation before it can
        proceed", which is exactly what reopening means.
      - `declined`/`obsolete` never shipped; the lifecycle reverses them to
        `untriaged` ("user reconsiders"), and has no edge to `revisit` at all.

    Both land in `claim-promotes`, so the ordinary claim that follows works.
    """
    target = "revisit" if statuses.has("shipped", current_status) else "untriaged"
    return f"endless task update E-{item_id} --status {target}"


def _warn_force_deprecated(verb: str, item_id: int, current_status: str) -> None:
    """Announce that `--force` is on its way out, and name what replaces it.

    E-2093. `--force` spelled two unrelated decisions on `claim` — demote a
    settled task, and claim with no Claude session to bind — and only the first
    was documented. One flag, two decisions, one of them invisible, is why it
    got reached for as the answer to "I cannot write", which is neither of
    them. `spawn --force` spelled only the first, so two verbs also disagreed
    about what the flag meant.

    Both halves are now named separately: `--unattended` for the session half,
    and an explicit status transition for the settled half. This release the
    flag still does what it did, so nobody's script breaks mid-cycle; the next
    one deletes it. It does NOT survive as an alias for either half — an alias
    that still spells two decisions is the defect.

    It takes `current_status` because the reopen route it prints is only a
    route from a settled status, and only to the status the lifecycle has an
    edge for. A deprecation notice that hands you a command your next call
    refuses is the same defect this task exists to remove, one layer out.

    stderr, not an exception: the point of the deprecation window is that the
    command still runs.
    """
    lines = [
        f"warning: `endless task {verb} --force` is deprecated and will be removed.",
    ]
    if verb == "claim":
        lines.append(
            "  Claiming with no Claude session to bind is now `--unattended`."
        )
    if current_status in _CLAIM_REFUSED_STATUSES:
        lines.append(
            f"  Re-{verb}ing settled work now means reopening it first, "
            f"then {verb}ing normally:"
        )
        lines.append(f"      {_settled_reopen_route(item_id, current_status)}")
        lines.append(f"      endless task {verb} E-{item_id}")
    else:
        lines.append(
            "  Its settled-status demotion is going away with no replacement "
            "flag; reopen a settled task explicitly instead."
        )
    click.echo(click.style("\n".join(lines), fg="yellow"), err=True)


# E-1555: statuses a task can be reopened from. `declined`/`obsolete` carry an
# explicit "we chose not to do this" decision — reversing them is an
# intentional act that should use `task update --status` and `--reason`,
# not a generic reopen. `unverified` is not terminal: it's "implementation done,
# trust pending" — reopening it would discard pending verification rather
# than reactivate completed work.
_REOPENABLE_TERMINAL_STATUSES: frozenset[str] = frozenset(statuses.get("reopenable"))


def _task_claimants(item_id: int) -> list[dict]:
    """Every session that ever claimed `item_id`, most recently active first.

    Reads `sessions.task_id`, which per ED-1560 is write-once — set at claim,
    never cleared, never repointed — and so is the DURABLE record of who owned
    the task. Deliberately unfiltered by `state`: an `ended` session is the whole
    point, because the session that worked a task is almost never still live by
    the time someone tries to spawn onto it again. The column carries no UNIQUE
    constraint, so several sessions may share a value and all of them are
    returned.

    NOT `session_tasks`. That table records INVOLVEMENT — how a task first
    entered a session's scope — so a session that merely read the task is in it
    and a session that claimed a task it had already filed still reads
    `surfaced`. Involvement is not ownership and cannot stand in for it.

    Ordered to match monitor.resumeByTask (`ORDER BY last_activity DESC`), which
    is what `session goto <task> --resume` resolves through, so the session this
    refusal names is the session that command lands in.
    """
    return db.query(
        "SELECT id AS eid FROM sessions WHERE task_id = ? "
        "ORDER BY last_activity DESC, id DESC",
        (item_id,),
    )


def _check_prior_claim(item_id: int, current_status: str) -> None:
    """Refuse a spawn onto a task some session already claimed (E-1967).

    The companion to `_check_task_ownership`, which runs first and covers the
    narrower case: a session holding the task RIGHT NOW. This one covers the case
    that missed, which is the defect — the prior session is usually `ended`, so
    the live check saw a free task and let a second session start over without
    the first one's reasoning.

    There is no escape hatch and none left to offer: `--force` governed the
    status demotion, not this, and E-2093 deprecated it outright; `--new-session`
    was dropped by E-1968; and `task release` is disabled, so a claim is not
    something anyone can undo. Working a task a prior session claimed means
    resuming that session.

    No exclusion for the spawning session, matching `_check_task_ownership`'s
    `current_eid=None`: spawn never claims ownership for the spawner. Under
    ED-1560 a session holds one task for its lifetime, so a session spawning onto
    a DIFFERENT task cannot itself be the target's claimant. Re-claiming your own
    task is legitimate, but that belongs to `claim`, not here.
    """
    claimants = _task_claimants(item_id)
    if not claimants:
        return

    most_recent = session_id_display(claimants[0]["eid"])
    task_ref = task_id_display(item_id)

    # `--revisit` / `--no-revisit` are E-1968's flags on `session goto`, and they
    # are accepted only when the task is settled. Rendering them unconditionally
    # would teach a flag the very next command rejects. Reached with a settled
    # status only via the deprecated `--force`, which skips the settled-status
    # gate above but not this one.
    settled = current_status in _REOPENABLE_TERMINAL_STATUSES
    revisit = " --revisit" if settled else ""

    lines = [
        f"{task_ref} was claimed by session {most_recent}. "
        f"Pick the work back up there:",
        f"    endless session goto {task_ref} --resume{revisit}",
    ]
    if settled:
        lines.append(
            "  (--no-revisit instead, to read it back without reopening "
            "the task.)"
        )
    if len(claimants) > 1:
        others = ", ".join(
            session_id_display(c["eid"]) for c in claimants[1:]
        )
        lines.append(f"  Earlier claimants: {others}")
    lines.append("")
    lines.append(
        f"Spawning a second session on it would start over without "
        f"{most_recent}'s reasoning, which is only in that session."
    )
    raise click.ClickException("\n".join(lines))


# E-1845: statuses from which a material description edit resets a task to
# `untriaged`. These are exactly the pre-work states — no implementation has
# started, so re-deciding what the task IS costs nothing but a second look.
#
# `ready` is deliberately included: approval was granted against the OLD
# description, so a rewrite should require re-approval rather than silently
# inherit it. `underway` is deliberately EXCLUDED: a live session is mid-flight
# and a description tweak must not yank the task out from under it. So are
# `unverified` and every terminal status, where re-triage means nothing.
_DESCRIPTION_RESET_FROM: frozenset[str] = frozenset(
    statuses.get("description-reset-from")
)


# The statuses that mean "nobody has decided this task is spec-complete yet" —
# the ones from which attaching a plan promotes to `submitted`. The promotion
# itself lives in the Go executor; this reads the same group the executor's
# `isPreJudgmentStatus` reads (E-1891), so it can no longer be a mirror that
# drifts — `--keep-status` needs to know whether the promotion is about to fire.
_PRE_JUDGMENT_STATUSES: frozenset[str] = frozenset(statuses.get("pre-judgment"))


def _perform_claim_work(
    item_id: int,
    title: str | None,
    current_status: str,
    target_session: int | None,
    proj_name: str,
    project_root: Path | None = None,
    unattended: bool = False,
):
    """Emit claim events, print status/binding/worktree lines, create the worktree.

    Returns (wt_path, created). Caller has already validated the
    done-ish-status gate and the multi-owner refusal — this helper only
    does the mutation half of a claim.

    target_session=None is the spawn pre-claim case (Claude not yet
    started); skips the task.claimed event entirely. SessionStart's
    spawn-marker auto-bind records the binding once Claude is up.

    `unattended` (E-2093) says target_session is None because there is no
    session to name — `task claim --unattended`, for manual work or cron, and
    (E-2106) a claim from a shell, whose session is about to be launched and so
    does not exist yet — rather than because the binding is merely deferred
    (spawn's pre-claim, where the spawner's own session is the right actor).
    The distinction matters at the event layer: `actor_kind="cli"` requires a
    resolvable session and refuses without one, which is why the old
    `claim --force`-with-no-session path was unreachable except by accident —
    it got past claim's own gate and then died inside `emit_event` with a
    message about a pane. A claim that deliberately has no session IS the
    `system` actor by that field's own definition ("cron / one-shot tools; no
    session expected"), so it says so.

    That is NOT this flag quietly doing the global `--no-session`'s job.
    `--no-session` downgrades attribution for EVERY event of an invocation,
    including ones that do have a session to name. This names the actor
    correctly for the one claim that genuinely has none.

    `project_root` defaults to cwd's project, which is right for every
    interactive claim. `session resume`'s task-less auto-claim (E-1918) passes
    it explicitly: there the project is a property of the RESUMED session, and
    the resuming shell can be standing anywhere.
    """
    from endless.event_bridge import emit_event
    from endless.worktree_cmd import create_task_worktree, _project_root

    # E-1401: pass the resolved session explicitly so emit_event doesn't
    # re-resolve via _current_endless_session_id (which would race the
    # binding we just established, or fail outright when called from a
    # plain shell during spawn pre-claim).
    session_id_arg = str(target_session) if target_session is not None else None
    actor_kind = "system" if unattended and target_session is None else "cli"

    # E-1500: secure the worktree FIRST. If creation refuses (orphan branch
    # carrying real work, a DB/file plan mismatch, an undeletable branch),
    # the task's status is left untouched rather than stranded underway.
    if project_root is None:
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
            actor_kind=actor_kind,
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


def create_claimed_task_for_session(
    *,
    title: str,
    description: str,
    project_name: str,
    project_root: Path,
    session_id: int,
) -> tuple[int, Path]:
    """Create a task already claimed by `session_id`, and give it a worktree.

    The container `session resume` mints for a session that never claimed a task
    (E-1918). Created straight at `underway` and bound in one step, skipping
    triage and the approve gate deliberately: a human ran `session resume`, so
    the approval that gate exists to capture already happened interactively —
    and triage could not judge a placeholder title anyway.

    `force=True` on the add is about the title, not the gates: the title is a
    fixed placeholder that does not open with a registered verb, and letting it
    fall through to the haiku verb-check would put a network call on the resume
    path (and mint a bogus verb if it answered YES).

    Returns (task_id, worktree_path).
    """
    item_id = add_item(
        title,
        description=description,
        project_name=project_name,
        status="underway",
        force=True,
    )
    wt_path, _ = _perform_claim_work(
        item_id=item_id,
        title=title,
        current_status="underway",
        target_session=session_id,
        proj_name=project_name,
        project_root=project_root,
    )
    return item_id, wt_path


def claim_item(item_id: int, unattended: bool = False, force: bool = False):
    """Claim ownership of a task and bind a Claude session to it.

    `unattended` (E-2093) claims with NO Claude session bound: manual work at a
    terminal, a plain shell, cron. It is one of the two decisions `--force`
    used to spell, and the one that is real; it was unreachable except by
    accident, since nothing documented that `--force` did it.

    It is NOT the global `--no-session`, and the two must not be confused.
    Traced for E-2093, because the names are close enough to be mistaken for
    each other and different enough to matter:

      - `--no-session` never reaches the session resolution below. Claim
        resolves a session and binds it exactly as it always does, so a claim
        run with `--no-session` from a Claude pane still gets a bound session.
      - What it does reach is the EVENTS claim emits: `emit_event` downgrades
        `actor.kind` to `system` and NULLs the envelope's `session_id`, so the
        claim is recorded as done by the system rather than by that pane.
      - The binding survives that, because the executor reads the session id
        out of the `task.claimed` PAYLOAD, not out of the envelope.

    That is the flag's documented meaning — attribution — applied to this verb
    like any other, not a third hidden behaviour. So the two are orthogonal:
    `--unattended` decides whether a session is BOUND, `--no-session` decides
    who the resulting events are ATTRIBUTED to, and neither silently does the
    other's job.

    `force` is DEPRECATED (E-2093) and still does what it always did for one
    release; see `_warn_force_deprecated`. Do not add callers.

    The settled-status gate no longer has a bypass. Re-claiming settled work
    means reopening it first, which is a status transition that says so; see
    `_settled_reopen_route`.

    Resolves the binding target as: (1) current Endless session via
    ENDLESS_SESSION_ID / TMUX_PANE; (2) single sibling Claude session in
    the same tmux window (auto-pick); (3) on a tty, multi-sibling case
    displays `endless session list --project <project>` and prompts for
    a session ID. Off-tty multi-sibling refuses loudly. If no session
    resolves and this is not `--unattended`: refuse.
    """
    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status, "
        "project_id FROM live_tasks "
        "WHERE id = ?",
        (item_id,),
    )
    if not row:
        raise click.ClickException(
            f"No task found with id {item_id}"
        )

    current_status = row[0]["status"]
    if force:
        _warn_force_deprecated("claim", item_id, current_status)
    if not force and current_status in _CLAIM_REFUSED_STATUSES:
        raise click.ClickException(
            f"E-{item_id} is in status '{current_status}'; re-claiming "
            f"would demote it to 'underway'.\n"
            "  To pick the work back up, reopen it first — then claim "
            "normally:\n"
            f"      {_settled_reopen_route(item_id, current_status)}\n"
            f"      endless task claim E-{item_id}\n"
            "  To attach this session to the task without changing its "
            "status (ownership\n  record + status bar, no worktree):\n"
            f"      endless task bind E-{item_id}"
        )

    _, proj_name = _resolve_project(None)
    target_session = _resolve_session_id_with_prompt(
        project_name=proj_name,
        prompt_verb="claimed for",
    )
    # E-2106 replaced the old "No Claude session available" refusal, which fired
    # for every caller that could not NAME a session — including the most
    # ordinary one there is, a user in a shell picking a task up. A session is
    # available to that caller; it just has not been started yet, and claim now
    # starts it. What has to be refused instead is a shell claim that cannot
    # start one safely, and that is a property of the WINDOW, not of session
    # resolution. Checked here, before `_perform_claim_work`, so a refusal
    # leaves no half-claimed task behind.
    if target_session is None and not (unattended or force):
        _require_tmux_for_claim(item_id)

    # E-2074 removed the "a background session may only claim `ready` work"
    # refusal that stood here. It gated on sessions.kind_id = background, and
    # background agents are gone, so no session could ever trip it again. The
    # rule it enforced — an unattended loop must not pick up work a human has
    # not approved — has no unattended loop left to bind.

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

    wt_path, _created = _perform_claim_work(
        item_id=item_id,
        title=row[0]["title"],
        current_status=current_status,
        target_session=target_session,
        proj_name=proj_name,
        # `target_session is None` reaches here only on E-2106's shell-in-tmux
        # path — the no-tmux case raised above, and every other caller resolved
        # something. At the moment this event is emitted that claim genuinely
        # has no session: the one about to be launched does not exist yet, and
        # binds itself from its cwd at SessionStart. So it is the `system`
        # actor by that field's own definition, exactly as `--unattended` is.
        # This is the EVENT actor only; what claim then PRINTS is decided
        # separately below, where an unattended claim and a shell claim differ.
        unattended=unattended or force or target_session is None,
    )

    _echo_claim_next_step(
        item_id,
        project_id=row[0]["project_id"],
        worktree=str(wt_path) if wt_path else None,
        unattended=unattended or force,
        bound_session=target_session,
    )


def _echo_claim_next_step(
    item_id: int,
    *,
    project_id: int,
    worktree: str | None,
    unattended: bool,
    bound_session: int | None,
) -> None:
    """End a claim with the ONE next step for the caller that ran it (E-2106).

    There used to be a "choose one" menu here. A menu is the right shape only
    when the caller genuinely has a choice, and this caller never did: which
    step applies is fully determined by where the claim ran from, and the two
    options offered were a command that refuses re-claims and a shell helper
    that does not exist. So this decides instead of asking.

      - inside a Claude session — it is already here, and only its working
        directory is in the wrong place. `/cd`, and nothing else.
      - `--unattended` — the caller said there is no Claude session and does not
        want one. The worktree path is the whole answer.
      - a shell that BOUND a Claude session in another pane (E-1242) — the
        session exists and now owns the task; what the caller needs is the way
        to it, which is `session goto`.
      - a shell that bound nothing — a shell cannot become the session, so one
        is started, in a window of its own so the caller's shell survives.

    A claim with no worktree (nothing to cd into, nothing to launch in) falls
    through silently; `_perform_claim_work` has already said what it did.
    """
    if worktree is None:
        return
    if unattended:
        return
    if _in_claude_session():
        click.echo("")
        # /cd points Claude's own working directory at the worktree, so every
        # tool (Read/Write/Edit + a fresh Bash) defaults to it instead of main.
        # Absolute path: /cd does not expand ~ or $(...). Until you run this, a
        # claimed session is refused tool use from main (E-1586).
        click.echo(
            f"  /cd {worktree}   "
            f"# point Claude's working dir at the worktree (do this first)"
        )
        return
    if bound_session is not None:
        click.echo("")
        click.echo(
            f"  E-{item_id} is bound to session "
            f"{session_id_display(bound_session)}, which is in another pane. "
            f"Go there to work it:"
        )
        click.echo(f"      endless session goto {session_id_display(bound_session)}")
        return
    _launch_claude_for_claim(item_id, project_id, worktree)


def bind_item(item_id: int) -> None:
    """Record this session as the owner of a task, without changing its status.

    E-2093 rewrote this description. It used to say "for status-bar display
    only", which understated the verb by a wide margin: bind sets
    `sessions.task_id`, and under ED-1560 that column IS the ownership record —
    write-once, never cleared, and the only route back to the session's
    transcript (`endless session goto E-<id> --resume` resolves through it).
    Setting ownership is not display. The understatement is why bind read as
    too small to be the answer when a session needed one.

    What it does NOT do, and what separates it from `claim`: it does not change
    the task's status, it does not create a worktree, and it does not change
    the session's STATE — the executor deliberately preserves a live state, so
    binding a task to an idle session leaves it idle. (Since E-2093 that no
    longer strands anybody: the session's next hook event wakes it, because it
    now holds a task. See monitor.WakeSession.)

    Use it to attach a session to a task whose status should not move —
    typically one already `assumed` / `confirmed` / `unverified`. To resume
    WORKING such a task, reopen it and claim: `task update <id> --status
    revisit`, then `task claim <id>`.

    Target session resolution mirrors `claim_item`: env var / pane-
    direct / single-sibling auto-pick / on-a-tty multi-sibling prompt.
    Refuses when no session resolves — bind without a session is
    meaningless (nothing for the status bar to display).

    FIRST-SET-ONLY (E-1968, per ED-1560). `sessions.task_id` is
    write-once: bind may fill a session that holds no task, but it may not move
    a session from one task to another. A session owns exactly one task for its
    lifetime; work on a different task is a different session. The refusal is
    here rather than only at the DB because E-1969's write-once trigger raises
    a SQLite abort, which is not an answer a user can act on.

    Emits a `task.claimed` event (the existing event added in E-1242);
    the Go executor performs the sessions DB write.
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status FROM live_tasks "
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

    held = db.query(
        "SELECT task_id FROM sessions WHERE id = ?",
        (target_session,),
    )
    already = held[0]["task_id"] if held else None
    if already is not None and already != item_id:
        raise click.ClickException(
            f"Session {target_session} already holds E-{already}, and a "
            f"session's task is set once and never moved.\n"
            f"To work E-{item_id}, use a different session:\n"
            f"    endless task spawn E-{item_id}"
        )
    if already == item_id:
        click.echo(
            click.style("•", fg="cyan")
            + f" E-{item_id} is already bound to session {target_session} "
              f"(task status unchanged: {current_status})"
        )
        return

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


# E-1968 / ED-1560: `task release` is DISABLED, not deleted. Disabled
# 2026-08-15.
#
# Release's defining act is clearing `sessions.task_id`, and that column
# is write-once — set at claim, never cleared and never repointed. Clearing to
# NULL and then setting a new value is reassignment through the back door, so
# release cannot survive as a workflow. E-1969 enforces this with a BEFORE
# UPDATE trigger that aborts on any change to a non-NULL task_id; without this
# refusal the verb would fail at the DB with a SQLite abort instead of an
# answer.
#
# The command and its CLI wiring are deliberately kept as a tombstone.
# RE-ENABLING MEANS DELETING THIS REFUSAL AND RESTORING THE BODY (see
# `git show` on the E-1968 commit) — do that only if a real use-case appears
# that the one-session-one-task invariant cannot serve. If none has appeared
# after several months, delete the verb, this function, and its CLI command
# outright.
def release_item(item_id: int | None, ignore_missing: bool = False) -> None:
    """Refuse: releasing a session's task is forbidden by the invariant.

    Kept as a tombstone so the verb answers instead of vanishing. See the
    comment above for the re-enabling criterion.
    """
    raise click.ClickException(
        "`endless task release` is deliberately disabled.\n"
        "A session's task is set once at claim and never cleared or "
        "moved — one session, one task, for the session's lifetime. "
        "Releasing would leave the task unowned while the session that "
        "worked it is still the only place its transcript lives.\n"
        "  To stop working and leave the task for someone else, hand it "
        "back by status:\n"
        "      endless task update E-<id> --status revisit\n"
        "  To work something else, start a session for it:\n"
        "      endless task spawn E-<other>"
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


def _reopen_task_core(item_id: int) -> tuple[str, str, bool]:
    """Reopen a terminal-status task back to `revisit`.

    Validates eligibility and emits `task.status_changed`. Caller renders the
    result line.

    E-1968: this used to emit `task.released` for whichever session held the
    task, clearing `sessions.task_id` as a silent side effect. Under
    ED-1560 that column is write-once — set at claim, never cleared and never
    repointed — so the binding now survives a reopen untouched. The loss it
    caused was real: E-1917's reopen cleared its binding a week after landing,
    after which both resume paths reported the session had never claimed a task
    (`task_id` alone cannot tell *released* from *never claimed*).

    Returns (prev_status, new_status, text_present).
    """
    from endless.event_bridge import emit_event

    row = db.query(
        "SELECT id, COALESCE(title, description) as title, status, text "
        "FROM live_tasks WHERE id = ?",
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

    # E-1889: reopen always lands `revisit`, whatever the plan text says.
    # A reopened task is by definition work whose prior judgment no longer
    # holds — either the plan was wrong or what shipped under it was — and
    # `revisit` is the status that means exactly that. Routing to `ready` on
    # text-present would have re-asserted a human approval nobody re-granted;
    # routing to `unplanned` on text-absent would have claimed the task was
    # never planned. The other two reopen paths (`session resume --reopen`,
    # E-1801) already landed `revisit`; this closes the divergence.
    #
    # `text_present` is still returned: callers render it as the message
    # suffix, which is the one place plan-vs-no-plan is still worth saying.
    text_present = bool((row[0]["text"] or "").strip())
    new_status = "revisit"

    _, proj_name = _resolve_project(None)

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
    """Flip a terminal-status task back to `revisit`.

    Task state only: it changes the status and nothing else. No worktree is
    created or touched, and an existing session→task binding is left exactly
    as it was — the session that worked the task stays reachable by
    `endless session goto E-<id> --resume` afterwards (E-1968).
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
        "       COALESCE((SELECT slug FROM task_types WHERE id = live_tasks.type_id), '') AS type, "
        "       phase, tier, parent_id, outcome, analysis "
        "FROM live_tasks WHERE id = ?",
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
    validate_fields(title=title, description=description, force=force,
                    action=NOTHING_CHANGED)

    # E-1658: when the title or type is being changed, re-gate the effective
    # (title, type) against the verb-category accepts map. Closes the
    # create-as-todo-then-flip-to-research bypass, which would otherwise strand a
    # task in an un-terminable state (an action-verb title under an investigation
    # type: research forbids 'confirmed'/'assumed'/'unverified' and the
    # completed-gate rejects the action verb). Fires only on an explicit
    # title/type edit, so an unrelated update to a pre-existing invalid row is
    # not retroactively blocked (mirrors the maybe-phase rule below).
    if title is not None or task_type is not None:
        effective_title = title if title is not None else row[0]["title"]
        effective_type = task_type if task_type is not None else row[0]["type"]
        _require_verb_category_for_type(effective_title, effective_type)

    # Validate status if provided. E-1956: read from the shared vocabulary
    # rather than a local copy — this tuple had drifted to omit `submitted`,
    # which every other surface accepts (`task submit` sets it), so
    # `task update --status submitted` was refused for no stated reason.
    if status is not None:
        if status not in TASK_STATUSES:
            raise click.ClickException(
                f"Invalid status '{status}'. "
                f"Valid: {', '.join(TASK_STATUSES)}"
            )
        # Use the incoming task_type if --type is also being set in this
        # update, else the existing type on the row.
        effective_type = task_type if task_type is not None else row[0]["type"]
        # E-1577/E-1579/E-1658: type→status validity. research/epic reject
        # 'unverified'/'assumed'/'confirmed'; todo/bugfix reject the review lane.
        # The Go transition table refuses todo/bugfix → completed (E-1658); a
        # `completed` flip on an implementation type is caught there.
        _require_status_allowed_for_type(status, effective_type)
        # E-1956: `obsolete` is refused on work that already shipped — the fact
        # to record there is a replaced_by relation, not a status that reads as
        # "never happened".
        _refuse_obsolete_on_shipped_work(item_id, status, row[0]["status"])

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

    # E-2120 removed the plan-edit auto-revisit (E-1762). It flipped a
    # terminal-status task to `revisit` whenever its plan text actually changed,
    # inferring "the work needs redoing" from an edit that, on a finished task,
    # usually records what shipped. That inference was calibrated when such an
    # edit was rare and therefore suspicious; the discovery rules in
    # `docs/guide/tasks.md` now REQUIRE a session to record grown scope on the
    # task it folded work into, so the common case inverted and the inference
    # became wrong more often than right.
    #
    # It also fired a user-owned edge. `{From: Assumed, To: Revisit, Actor:
    # ActorUser, "reopens — shipped work found wrong"}` in
    # internal/taskstatus/transitions.go (with twins from Confirmed and
    # Completed) is the transition it drove, and `revisit` cannot reach a
    # terminal status again — so documenting what shipped destroyed the
    # verification the user had granted, recoverable only by walking the task
    # back through underway/unverified for them to re-close by hand.
    #
    # Reopening keeps its explicit spelling, `--status revisit`, and stays the
    # user's to make. The edges are untouched; only the inference is gone.

    # E-1845: a material description edit resets a pre-work task to `untriaged`.
    # The description IS the spec that triage (untriaged → unplanned/submitted)
    # and approval (submitted → ready) were judged against, so rewriting it
    # invalidates those judgments — the task has to be looked at again. Guards
    # (they were shared with the auto-revisit E-2120 removed above):
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
    # only the one guarded above. The plan-attach promotion (a pre-judgment task
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
    # whose plan text was merely typo-fixed. `status is None` is not re-checked
    # here: passing both --status and --keep-status was rejected at the top of
    # this function.
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

    # E-2120: audience-gate the status render. An agent is shown the fields it
    # ASKED to change; a status entry it did not ask for — the E-1845
    # description-edit reset, the tier-1 advance — is a completed, correct
    # transition it can do nothing about, and every one of them got relayed to
    # the user as if it were news, spending the scarcest resource in the loop.
    # Rewording that output was tried (E-1859) and did not take: the stimulus is
    # the PRESENCE of agent-addressed text about a status, not its phrasing, so
    # the fix has to be the audience gate rather than a third rewrite.
    #
    # A human running the command interactively still sees all of it, unchanged
    # — there the status line and the advisory below are the useful part.
    #
    # `status is None` is the whole "did it ask for this" test: an explicit
    # --status is a field the agent named, and every other status entry in
    # `changes` was inferred from some other edit.
    #
    # The gate is `agent_help.agent_facing()` rather than a fourth spelling of
    # the same question — E-1966, E-2006 and E-2097 each folded a competing
    # spelling into that one function.
    agent_reading = agent_help.agent_facing()
    rendered = (
        [c for c in changes if c[0] != "status"]
        if agent_reading and status is None
        else changes
    )

    # Header title reflects the new title if it was changed in this update.
    header_title = fields.get("title", row[0]["title"]) or row[0]["description"]
    _emit_field_changes(item_id, header_title, rendered)

    # E-1845: the field render above already shows `Status: <old> ->
    # untriaged`; this names WHY and the escape hatch. Human-only (E-2120):
    # an agent is shown the fields it asked to change and nothing else.
    if auto_untriage and not agent_reading:
        because = (
            "re-spec'd and re-planned in one call"
            if plan_attached
            else "the description is the spec that triage and approval were "
                 "judged against"
        )
        # E-1859 (reopened): the offer here is about COST, not about whether the
        # transition was right. The previous wording ended "pass --keep-status
        # to suppress", which read as an undo offered after the fact and invited
        # agents to relay a completed, correct transition to the user as a
        # decision to accept — observed four times in one session. `--keep-status`
        # is a spend control: every re-triage is a model call, and a filer who
        # already knows the edit was cosmetic should skip paying for one.
        if untriage_target == "untriaged":
            click.echo(
                f"{task_id_display(item_id)} → untriaged; re-triage will run "
                f"(one model call). Pass --keep-status on a typo- or "
                f"formatting-only edit to skip it."
            )
        else:
            click.echo(
                f"{task_id_display(item_id)} → {untriage_target} ({because})."
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
    # Raw `tasks`, NOT live_tasks (E-1929). This is the payoff for retaining a
    # removed row: `task show E-NNN` on an id that is now a hole explains itself
    # — who filed it, what it said, that it was removed — instead of erroring as
    # if the id had never existed. Every OTHER read here goes through live_tasks.
    row = db.query(
        "SELECT t.id, t.title, t.description, t.analysis, t.text, t.phase, t.status, "
        "COALESCE(tt.slug, '') AS type, "
        "t.parent_id, t.source_file, t.created_at, t.updated_at, "
        "t.completed_at, t.sort_order, t.tier, t.outcome, t.removed, "
        "p.name as project_name "
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
            # E-1956/E-1185: emitted ungated (a terminal status is a display
            # rule; this is data) and always present, so an absent key never has
            # to be read as "not replaced" / "not a duplicate".
            "replaced_by": [
                f"E-{i}" for i in replaced_by_map([item_id]).get(item_id, ())
            ],
            "duplicates": [
                f"E-{i}" for i in duplicates_map([item_id]).get(item_id, ())
            ],
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
            # E-1929: the row is retained after `task remove`, so a consumer must
            # be able to tell a removed task from a live one. Always present, so
            # the absence of the key never reads as "live".
            "removed": bool(item["removed"]),
        }
        if show_children:
            children = db.query(
                "SELECT id, COALESCE(title, description) as title, status, phase "
                "FROM live_tasks WHERE parent_id = ? "
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
        if item["removed"]:
            # First line after the title, before anything else (E-1929): every
            # field below describes a task that no longer exists, and an agent
            # that skims must not act on it.
            click.echo("removed=true")
        click.echo(f"project={item['project_name']}")
        tier_str = f" tier={tier_display(item['tier'])}" if item["tier"] else ""
        # E-1956: key=value rather than the human view's parenthetical, so the
        # line stays parseable — but on the status line, not buried in `links=`,
        # because a terminal status read without it is misleading on its own.
        replaced_ids = replaced_by_map([item_id]).get(item_id)
        rb_str = (
            " replaced_by=" + ",".join(f"E-{i}" for i in replaced_ids)
        ) if replaced_by_note(item["status"], replaced_ids) else ""
        duplicate_ids = duplicates_map([item_id]).get(item_id)
        dup_str = (
            " duplicates=" + ",".join(f"E-{i}" for i in duplicate_ids)
        ) if duplicates_note(item["status"], duplicate_ids) else ""
        click.echo(f"type={item['type']} phase={item['phase']} "
                    f"status={item['status']}{tier_str}{rb_str}{dup_str}")
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
                "FROM live_tasks WHERE parent_id = ? "
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

    # E-1929: `task remove` retains the row, and this command is the one place it
    # is still visible. Say so LOUDLY and FIRST — the fields below describe a task
    # that no longer exists, and reading them as current is the whole risk of
    # retaining it.
    if item["removed"]:
        click.echo(click.style("⊘ REMOVED — this task no longer exists.", fg="red", bold=True))
        click.echo(click.style(
            "  Its id is retained so nothing can be re-filed under it.", dim=True))

    click.echo(f"{label('ID:')} {val(task_id_display(item['id']))}")
    click.echo(f"{label('Title:')} {val(item['title'])}")
    click.echo(f"{label('Project:')} {val(item['project_name'])}")
    click.echo(f"{label('Type:')} {val(item['type'])}")
    click.echo(f"{label('Phase:')} {val(item['phase'])}")
    # E-1956: a terminal status reads as the end of the story, so when the task
    # was superseded that fact rides along with it rather than living only in
    # the 'This task:' block below. Dim: it annotates the status, it is not a
    # second value competing with it. E-1185 adds `duplicates` on the same rule —
    # a task closed BECAUSE it duplicated another has exactly that problem.
    status_note = status_notes(
        item["status"],
        replaced_by_map([item_id]).get(item_id),
        duplicates_map([item_id]).get(item_id),
    )
    click.echo(
        f"{label('Status:')} {val(item['status'])}"
        + click.style(status_note, dim=True)
    )
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
            "FROM live_tasks WHERE parent_id = ? "
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

# The collapsed bucket every terminal status folds into in the children-state
# breakdown (E-1567): todo/bugfix land on confirmed/assumed, research/epic on
# completed (E-1577/E-1537 §3), and obsolete/declined are universal terminals.
# A bucket label, not a status — which is why it is appended below rather than
# living in the registry.
_TERMINAL_BUCKET = "terminal"

# Finished-or-abandoned work. One set, two readers: the children-state
# breakdown collapses these into _TERMINAL_BUCKET, and relation rows colour
# them green (E-1477). E-1891 collapsed the second reader's byte-identical
# copy — `_RELATION_TERMINAL_STATUSES` — into this name.
_TERMINAL_STATUSES = frozenset(statuses.get("terminal"))

# Display order for the children-state breakdown: the non-terminal statuses in
# lifecycle progression, then the collapsed terminal bucket last.
#
# E-1891: derived, not typed. This tuple used to omit `submitted`, so a
# submitted child was counted in the "(N total)" suffix but rendered no bucket —
# the breakdown silently failed to reconcile, contradicting this very comment.
# taskstatus asserts that `children-state-order` and `terminal` partition the
# vocabulary, so every status now has exactly one bucket by construction.
_CHILDREN_STATE_ORDER = statuses.get("children-state-order") + (_TERMINAL_BUCKET,)


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
        "SELECT status, count(*) AS n FROM live_tasks WHERE parent_id = ? "
        "GROUP BY status",
        (parent_id,),
    )
    counts: dict[str, int] = {}
    total = 0
    for row in rows:
        status = row["status"]
        n = row["n"]
        total += n
        bucket = _TERMINAL_BUCKET if status in _TERMINAL_STATUSES else status
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
                   parent_id: int | None = None) -> str:
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

    E-2074 removed the `bg=True` variant along with background agents. It
    rendered a headless-agent preamble telling the agent to work the task, flip
    it to `unverified`, and stop, since nobody was watching the window.

    E-1968 removed the `respawn=True` variant along with `task spawn --reopen`.
    It rendered a distinct interrogative handoff for a task being reopened into
    a FRESH session, summarizing the prior session's outcome and last status
    snapshot. Reopening now resumes the prior session's actual transcript
    (`session goto <ref> --resume --revisit`), which needs no summary of itself.
    """
    import json
    import subprocess
    from endless.event_bridge import _resolve_endless_go

    effective_type = task_type if task_type in _HANDOFF_TYPES else "todo"
    child_rows = db.query(
        "SELECT count(*) AS n FROM live_tasks WHERE parent_id = ?",
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
        # E-1953: whether this project runs the minimizer's report channel. A
        # project that switched it off must not be handed the reporting
        # instructions at all — they would cost every spawned session a per-turn
        # model round trip that nothing enforces and nothing reads.
        "report_gate": _report_gate_on(),
    }
    template_name = f"handoff/{effective_type}"
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


def _report_gate_on() -> bool:
    """Whether the enclosing project runs the report channel (E-1953).

    Not handoff-specific: two emitters ask this now — the spawn handoff, which
    omits the reporting instructions entirely where the channel is off, and the
    wind-down nudge (E-1966). Anything that tells a session the channel is live
    asks here first.

    Defaults to True when no project root can be resolved, matching the Go
    side: the channel ships on, and an unresolvable root is ignorance rather
    than an opt-out. An emitter that silently dropped the instructions because
    a path lookup failed would leave sessions ungoverned in a project that
    wanted the gate — the failure direction that actually costs something.

    Resolved from CWD, nearest-config-wins, because that is what the Stop hook
    does (E-2030). This used to read `enclosing_project_root`, which maps a
    worktree back to the main checkout — so a worktree that had switched the
    minimizer on for itself was told the channel was off by every Python emitter
    while the Go hook held its turns against it.
    """
    from endless import config
    return config.report_gate_for_cwd()


def show_handoff(item_id: int):
    """Render the spawn handoff for a task and print it."""
    row = db.query(
        "SELECT t.id, t.title, t.parent_id, COALESCE(tt.slug, '') AS type_slug "
        "FROM live_tasks t LEFT JOIN task_types tt ON tt.id = t.type_id "
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


def tmux_window_name(item_id: int) -> str:
    """The tmux window name for a task: the task id, and nothing else (E-2102).

    A tab is narrow and a window name is the only per-window label the user
    sees, so it spends none of that width on a project name or a title slug the
    user already knows. Both windows Endless opens for a task — `task spawn`'s
    and `session goto --resume`'s — go through here, so the two cannot drift
    into naming the same thing differently.

    Do not reintroduce a richer format without re-checking two constraints the
    bare id satisfies by construction:

      - tmux parses ':' as session:window and '.' as window.pane in `-t`
        targets, so either character in a window name breaks
        `select-window -t <name>` / `send-keys -t <name>` even within one
        session. `E-NNNN` contains neither.
      - `internal/sandboxcmd/reapguard.go` reads window names back to decide
        which DB sandboxes to spare. Change the format and that regex changes
        with it, or a sandbox gets reaped while its window is open.
    """
    return task_id_display(item_id)


def _claude_binary() -> str:
    """Resolve the `claude` binary path, avoiding shell function wrappers.

    Prefers `~/.local/bin/claude` if present (the canonical install location),
    else falls back to `claude` on PATH. Used by the spawn flow.
    """
    claude_bin = os.path.expanduser("~/.local/bin/claude")
    if not os.path.exists(claude_bin):
        claude_bin = "claude"
    return claude_bin


def spawn_plan(item_id: int, project_name: str | None = None,
               worktree: str | None = None, force: bool = False,
               permission_mode: str = "auto", model: str | None = None,
               name: str | None = None):
    """Spawn a new tmux window with Claude working on a task's prompt.

    Pre-claims the task (status flip + worktree creation) BEFORE launching
    Claude, so the spawned session lands in a worktree on a task that is
    already underway. The SessionStart hook reads
    `@endless_spawned_by` from the new tmux window and records the
    session→task binding via `BindSessionToTask` (no redundant status
    flip). See E-1274.

    The foreground path launches Claude as the tmux window's *command* via the
    `endless-go spawn-window` launcher (E-1705): the handoff is delivered as
    claude's positional prompt argument, not typed in with send-keys. Spawned
    sessions default to `--permission-mode auto` (override with `permission_mode`;
    `model`/`name` are optional claude pass-throughs). There is no plan-mode step
    — a positional prompt leaves no interactive turn to type a slash-command into.

    E-2074 removed the `bg` and `attach` parameters with background agents.
    `bg` dispatched headless via `claude --bg` instead of opening a window;
    `attach` opened a window onto an already-live one. tmux is now the only
    delivery surface, so its presence is required unconditionally below.

    `force` is DEPRECATED (E-2093). It bypassed the settled-status demotion and
    nothing else — unlike claim's `--force`, which spelled a second decision as
    well — and it still does so for one release while warning. What replaces it
    is reopening the task explicitly; the refusal below names that route.
    """
    import shutil
    import subprocess
    import tempfile

    # tmux is the only delivery surface (E-2074 removed the headless path), so
    # the requirement is unconditional.
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
        "proj.path as project_path, "
        "COALESCE(tt.slug, '') AS type_slug "
        "FROM live_tasks p "
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

    if force:
        _warn_force_deprecated("spawn", item_id, current_status)

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

    # Mirror claim's done-ish-status gate
    if not force and current_status in _CLAIM_REFUSED_STATUSES:
        if current_status in _REOPENABLE_TERMINAL_STATUSES:
            # E-1968 rewrote this. It used to offer two routes, and both were
            # wrong: `--reopen` is retired, and `task reopen E-NNNN` first was
            # always the worse of the two — it moves the task to `revisit`,
            # outside _CLAIM_REFUSED_STATUSES, so the follow-up plain spawn
            # proceeds with no prompt at all. The message routed the user into
            # the trap it had just warned them about. The right move on settled
            # work is to pick up the session that did it, not to start a second
            # one that cannot see its reasoning.
            raise click.ClickException(
                f"E-{item_id} is '{current_status}' — settled work. Pick it "
                f"back up in the session that did it:\n"
                f"    endless session goto E-{item_id} --resume --revisit\n"
                f"  (--no-revisit instead, to read it back without reopening "
                f"the task.)"
            )
        # E-2093: the demotion bypass is going, so this no longer offers
        # `--force`. It names the same route claim's refusal does — reopen
        # explicitly, then spawn normally — so the two verbs stop disagreeing
        # about what settled work costs to pick back up.
        raise click.ClickException(
            f"E-{item_id} is in status '{current_status}'; spawning "
            f"would demote it to 'underway'.\n"
            "  Reopen it first, then spawn normally:\n"
            f"      {_settled_reopen_route(item_id, current_status)}\n"
            f"      endless task spawn E-{item_id}"
        )

    # Refuse if another live session already owns the task. Passing
    # current_eid=None treats any owner as "other" — spawn never claims
    # ownership for the spawning session. The reopen path already ran its own
    # liveness guard above (it navigates instead of raising), so this
    # raise-on-conflict check is for the non-reopen spawn only.
    _check_task_ownership(item_id, current_eid=None)

    # Then refuse if any session EVER claimed it, live or not (E-1967). Order
    # matters: the live check above owns the "someone is working this right now"
    # case and says so more specifically, so it runs first.
    _check_prior_claim(item_id, current_status)

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

    if cd_target is None:
        cd_target = str(wt_path)

    # Spawner identity for the @endless_spawned_by marker. Prefer the
    # current Endless session id; fall back to a pid-prefixed value so
    # non-Claude spawners (CLI from a plain shell) still set a non-empty
    # marker that SessionStart can key off.
    spawner_id = _current_endless_session_id() or f"pid-{os.getpid()}"

    window_name = tmux_window_name(item_id)

    # Render the handoff from the template (no stored prompt — E-1469) and
    # write it to a temp file for tmux load-buffer. cd_target is the worktree
    # the spawned session lands in.
    handoff_text = render_handoff(
        item_id, title,
        worktree_path=cd_target,
        branch=_branch_for_worktree(cd_target),
        task_type=item["type_slug"] or None,
        parent_id=item["parent_id"],
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


def search_tasks(
    query: str,
    project_name: str | None = None,
    show_all: bool = False,
    status_filter: list[str] | None = None,
    phase_filter: str | None = None,
    parent_id: int | None = None,
    search_text: bool = False,
    limit: int | None = None,
    no_limit: bool = False,
    llm: bool = False,
    as_json: bool = False,
):
    """Search tasks by query string across ID, title, and description.

    The cap is applied to the fetched rows rather than pushed into SQL (E-2071):
    a `LIMIT 20` query cannot tell you it matched 60, and "20 match(es)" under a
    silently truncated table is the exact sentence that produced two false
    "no existing task" conclusions.
    """
    cap = rowcap.resolve_cap(limit, no_limit, machine=as_json)
    project_id, proj_name = _resolve_project(project_name)

    where = "WHERE t.project_id = ?"
    params: list = [project_id]

    if status_filter:
        placeholders = ",".join("?" for _ in status_filter)
        where += f" AND t.status IN ({placeholders})"
        params.extend(status_filter)
    elif not show_all:
        where += f" AND t.status NOT IN ({statuses.sql_list('terminal')})"
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

    rows = db.query(
        f"SELECT t.id, t.phase, COALESCE(t.title, t.description) as title, "
        f"t.status "
        f"FROM live_tasks t "
        f"{where} "
        f"ORDER BY t.updated_at DESC",
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

    total = len(rows)
    rows, hidden = rowcap.cap_rows(rows, cap)

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
        rowcap.echo_footer(hidden, llm=True, err=True)
        return

    if llm:
        click.echo(f"# {proj_name} search: {query}")
        for row in rows:
            click.echo(
                f"E-{row['id']} {row['phase']} "
                f"{row['status']} {row['title']}"
            )
        rowcap.echo_footer(hidden, llm=True)
        return

    click.echo()
    click.echo(
        click.style(f"Search results for '{query}' ({proj_name}):", bold=True)
    )
    _render_flat_table(rows)
    rowcap.echo_footer(hidden)
    click.echo()
    # `total`, not len(rows): the count under a capped table has to be the count
    # of MATCHES, or the reader learns only how tall the table is.
    click.echo(click.style(f"{total} match(es)", dim=True))


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
            "SELECT id FROM live_tasks WHERE id = ?",
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
            "SELECT id FROM live_tasks WHERE id = ?",
            (children_of,),
        )
        if not row:
            raise click.ClickException(
                f"Source parent {task_id_display(children_of)} not found."
            )

        # Count children
        count = db.scalar(
            "SELECT count(*) FROM live_tasks WHERE parent_id = ?",
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
        "SELECT id, parent_id, phase FROM live_tasks WHERE id = ?",
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
    # `state` is deliberately not named here (E-2105). internal/schema/schema.sql
    # declares `state TEXT NOT NULL DEFAULT 'working'`, so the column the schema
    # already owns supplies it; restating the value on this side made Python a
    # fifth copy of the vocabulary for no gain.
    cursor = db.execute(
        "INSERT INTO sessions (session_id, platform) VALUES (?, 'claude')",
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
        if not db.exists("SELECT 1 FROM live_tasks WHERE id = ?", (tid,)):
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


def replace_task(
    old_id: int,
    new_id: int,
    status: str | None = None,
    outcome: str | None = None,
):
    """Mark old_id as replaced by new_id: record the relation, set the status.

    `status` is what the REPLACED task becomes. None means "derive it from what
    the task is now" (E-1956): work that already SHIPPED keeps the status it
    earned — the supersession is carried by the `replaced_by` relation, and
    overwriting a true terminal with `obsolete` would assert the work never
    happened — while everything else takes the historical 'obsolete' default.
    An explicit status still wins, subject to the same shipped-work guard.
    """
    from endless.event_bridge import emit_event

    if old_id == new_id:
        raise click.ClickException("A task cannot replace itself.")
    for tid in (old_id, new_id):
        if not db.exists("SELECT 1 FROM live_tasks WHERE id = ?", (tid,)):
            raise click.ClickException(f"Task {task_id_display(tid)} not found.")

    # Read the old row BEFORE anything is written: it decides the default status
    # and feeds the guard, and both must run while the call can still be refused
    # without leaving a half-applied relation behind.
    old_row = db.query(
        "SELECT COALESCE(title, description) as title, status "
        "FROM live_tasks WHERE id = ?", (old_id,)
    )[0]
    old_status = old_row["status"]

    if status is None:
        status = old_status if old_status in _SHIPPED_STATUSES else "obsolete"
    _require_outcome_for_declined(status, outcome)
    _refuse_obsolete_on_shipped_work(old_id, status, old_status, via_replace=True)

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

    changes = []
    _, proj_name = _resolve_project(None)
    # A status_changed event whose old and new are the same value would write a
    # no-op transition into the ledger and misreport a held status as a change,
    # so the held case routes any outcome through fields_updated instead — the
    # outcome still lands, the status history stays honest.
    if status != old_status:
        payload = {
            "old_status": old_status,
            "new_status": status,
            "cascade": False,
        }
        if outcome:
            payload["outcome"] = outcome
        emit_event(
            kind="task.status_changed",
            project=proj_name,
            entity_type="task",
            entity_id=str(old_id),
            payload=payload,
        )
        changes.append(("status", old_status, status))
    elif outcome:
        emit_event(
            kind="task.fields_updated",
            project=proj_name,
            entity_type="task",
            entity_id=str(old_id),
            payload={"fields": {"outcome": outcome}},
        )

    if outcome and outcome.strip():
        _mirror_doc_to_worktree(old_id, "outcomes", "outcome", outcome)

    if outcome:
        changes.append(("outcome", None, outcome))
    _emit_field_changes(
        old_id,
        old_row["title"],
        changes,
        suffix=f"(replaced by {task_id_display(new_id)})",
    )
    if status == old_status:
        # The header alone would read as "nothing happened". Say what was kept
        # and why, so the held status is visibly a decision rather than a miss.
        click.echo(click.style(
            f"  status held at {old_status!r} — shipped work keeps what it "
            f"earned; the supersession is the relation.", dim=True))


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
        "JOIN live_tasks t_src ON t_src.id = td.source_id "
        "JOIN live_tasks t_tgt ON t_tgt.id = td.target_id "
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




def _relation_map(item_ids, dep_type: str, note_col: str, other_col: str) -> dict[int, list[int]]:
    """Map each id in `item_ids` to the ids on the far end of a `dep_type` row.

    `note_col` is the task_deps column holding the task the annotation lands on;
    `other_col` holds the task it points at. Joined to live_tasks so a removed
    far end is never named.

    Batched over the whole id set on purpose (E-1956): this feeds table
    renderers, which would otherwise issue one query per row.

    One query shape, parameterized, rather than one spelled-out copy per
    relation: the two callers below differ ONLY in which column is which, so a
    swap is the single mistake available here, and stating the columns as
    arguments puts that swap where it can be read side by side instead of
    diffed out of two near-identical blobs. Both column names are module-local
    constants from the two call sites; nothing user-supplied reaches the SQL.
    """
    ids = list(item_ids)
    if not ids:
        return {}
    placeholders = ",".join("?" for _ in ids)
    rows = db.query(
        f"SELECT td.{note_col} AS noted_id, td.{other_col} AS other_id "
        "FROM   task_deps td "
        f"JOIN   live_tasks t ON t.id = td.{other_col} "
        "WHERE  td.source_type = 'task' AND td.target_type = 'task' "
        f"AND    td.dep_type = ? AND td.{note_col} IN ({placeholders}) "
        f"ORDER BY td.{other_col}",
        (dep_type, *ids),
    )
    out: dict[int, list[int]] = {}
    for row in rows:
        out.setdefault(row["noted_id"], []).append(row["other_id"])
    return out


def replaced_by_map(item_ids) -> dict[int, list[int]]:
    """Map each id in `item_ids` to the ids of the tasks that replace it.

    `old replaced_by new` is stored active-voice as (source=new, target=old,
    dep_type='replaces'), so a task's replacements are the source_ids of the
    'replaces' rows pointing AT it — the note lands on the TARGET.
    """
    return _relation_map(item_ids, "replaces", "target_id", "source_id")


def duplicates_map(item_ids) -> dict[int, list[int]]:
    """Map each id in `item_ids` to the ids of the tasks it duplicates.

    E-1185. The arguments are `replaced_by_map`'s, swapped, and that is the
    whole difference: both annotate the task that gets CLOSED, but that task
    sits on the opposite end of each relation. `dupe duplicates keeper` stores
    (source=dupe, target=keeper) and it is the DUPE that is closed, so the note
    lands on the SOURCE and points at the target.
    """
    return _relation_map(item_ids, "duplicates", "source_id", "target_id")


def _supersession_note(status: str | None, ids: list[int] | None, phrase: str) -> str:
    """The inline ' (<phrase> E-NNN)' annotation for a status display, or ''.

    E-1956: rendered ONLY alongside a TERMINAL status. A terminal status is the
    one that reads as the end of the story — `obsolete` as "never happened",
    `assumed` as "done, nothing follows" — so that is exactly where dropping the
    supersession loses information a reader cannot recover from the row. An open
    task's relations are still carried by `task show`'s 'This task:' block, and
    leaving them off the open rows keeps every default listing (which excludes
    terminal statuses) rendering as it did before.
    """
    if not ids or status not in _TERMINAL_STATUSES:
        return ""
    return f" ({phrase} " + ", ".join(task_id_display(i) for i in ids) + ")"


def replaced_by_note(status: str | None, ids: list[int] | None) -> str:
    """The inline ' (replaced by E-NNN)' annotation for a status display, or ''."""
    return _supersession_note(status, ids, "replaced by")


def duplicates_note(status: str | None, ids: list[int] | None) -> str:
    """The inline ' (duplicates E-NNN)' annotation for a status display, or ''.

    E-1185: one token — `duplicates` — across the human, --llm and --json
    renderings, as `replaced_by` has. It reads as a verb phrase with the row as
    its subject: "E-986  obsolete (duplicates E-1086)".
    """
    return _supersession_note(status, ids, "duplicates")


def status_notes(
    status: str | None,
    replaced_ids: list[int] | None = None,
    duplicate_ids: list[int] | None = None,
) -> str:
    """Every inline annotation a status cell carries, in one string.

    A task can be both superseded and a duplicate; the notes compose rather than
    one winning. Ordered replaced-by first because that is the relation the
    reader has been seeing since E-1956.
    """
    return (replaced_by_note(status, replaced_ids)
            + duplicates_note(status, duplicate_ids))


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
        color = "green" if r["status"] in _TERMINAL_STATUSES else "yellow"
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

def _finished_session_states() -> tuple[str, ...]:
    """States meaning the session is no longer in flight.

    Rendered green in the relations block, like a terminal task status;
    everything else is in flight. Derived as the complement of the `live` group
    rather than typed out (E-2105), so a state added in Go is classified once,
    there, and lands on the right side of this line without an edit here.
    `gone` belongs with them: a touch whose session record is absent can't be
    live either.
    """
    live = session_states.get("live")
    return tuple(
        state for state in session_states.all_states() if state not in live
    ) + (_MISSING_SESSION_STATE,)


# The relation a session holding `sessions.task_id = <this task>` renders as,
# whatever `session_tasks` recorded for the pair (E-1967). `sessions.task_id` is
# the OWNERSHIP record — write-once per ED-1560, never cleared — while
# `session_tasks` records INVOLVEMENT: how the task first entered the session's
# scope. The two answer different questions, and only the first one answers
# "who claimed this", which is what the `task spawn` guard refuses on.
_CLAIMED_SLUG = "claimed"
_CLAIMED_LABEL = "Claimed"


def _session_touches(item_id: int) -> list[dict]:
    """Every session that touched this task, most-recent touch first (E-1866).

    One row per session, carrying the session id, how the task entered that
    session's scope (claimed / surfaced / revisited, per ED-1497), the session's
    current task, and its state. Ordered by touch recency because the block
    exists for navigation — the session worth jumping to is almost always the one
    that touched the task last — which is deliberately *not* the id-ascending
    order of the 'This task:' relations block.

    Two sources, unioned (E-1967):

      - `session_tasks`, one row per (session, task) touch. LEFT JOINs
        throughout: relation_id is NULL for pre-E-1462 rows, and the sessions row
        may be gone entirely (session_tasks has no FK by design).
      - `sessions` itself, for every session bound to this task that has NO
        session_tasks row. That is not a rare corner: the touch is recorded from
        the event's ACTOR, so a claim driven from a plain shell — actor kind
        `cli`, no session id — binds the session without recording a touch. 149
        such bindings exist in this project's own database. Reading only
        session_tasks is what made "which sessions claimed this" unanswerable.

    A session bound to this task renders `Claimed:` either way, overriding
    whatever session_tasks says for the pair. `relation_id` is upgrade-only
    (E-1696) but never revised downward or re-stamped, so a session that filed a
    task in 2026-08 and claimed it in 2026-09 still reads `Surfaced` on the row
    written at filing time. `sessions.task_id` is the record that cannot lie, and
    reading the label off it is what makes this block agree with the `task spawn`
    guard by construction — both consult the same column.

    `touch_slug` preserves the raw session_tasks relation underneath the
    override, because the Created: line is derived from a `surfaced` touch and a
    session that surfaced a task and then claimed it must not lose its credit for
    filing it.
    """
    rows = db.query(
        "SELECT * FROM ("
        "  SELECT st.session_id AS session_id, "
        "         st.created_at AS first_touch, "
        "         st.updated_at AS last_touch, "
        "         r.slug        AS rel_slug, "
        "         r.label       AS rel_label, "
        "         s.state       AS state, "
        "         s.task_id     AS task_id, "
        "         s.session_id  AS uuid "
        "  FROM session_tasks st "
        "  LEFT JOIN session_task_relations r ON r.id = st.relation_id "
        "  LEFT JOIN sessions s ON s.id = st.session_id "
        "  WHERE st.task_id = ? "
        "  UNION ALL "
        "  SELECT s.id AS session_id, "
        "         s.started_at AS first_touch, "
        "         COALESCE(s.last_activity, s.started_at) AS last_touch, "
        "         NULL AS rel_slug, "
        "         NULL AS rel_label, "
        "         s.state AS state, "
        "         s.task_id AS task_id, "
        "         s.session_id AS uuid "
        "  FROM sessions s "
        "  WHERE s.task_id = ? "
        "    AND NOT EXISTS (SELECT 1 FROM session_tasks st2 "
        "                    WHERE st2.session_id = s.id "
        "                      AND st2.task_id = s.task_id) "
        ") ORDER BY last_touch DESC, session_id DESC",
        (item_id, item_id),
    )
    return [
        {
            "session_id": row["session_id"],
            "first_touch": row["first_touch"],
            "last_touch": row["last_touch"],
            # The displayed relation: ownership overrides involvement.
            "rel_slug": (
                _CLAIMED_SLUG if row["task_id"] == item_id else row["rel_slug"]
            ),
            "rel_label": (
                _CLAIMED_LABEL if row["task_id"] == item_id
                else (row["rel_label"] or _UNCLASSIFIED_TOUCH_LABEL)
            ),
            # The raw session_tasks relation, unoverridden — see the docstring.
            "touch_slug": row["rel_slug"],
            "state": row["state"] or _MISSING_SESSION_STATE,
            "task_id": row["task_id"],
            # The Claude UUID, for the transcript marker (E-2106). NULL when
            # session_tasks names a session whose sessions row is gone.
            "uuid": row["uuid"],
        }
        for row in rows
    ]


def _creating_session(touches: list[dict]) -> dict | None:
    """The touch that created the task, or None (E-1866).

    A task created inside a session gets a `surfaced` session_tasks row (per
    ED-1497 the relation is stamped at capture time; E-1696 made it upgrade-only,
    so a later claim or edit still never rewrites it downward). Absent for a task
    filed outside any session and for pre-E-1462 rows, whose relation is NULL —
    in both cases the Created: line stays as it was. The earliest touch wins if
    more than one session ever surfaced the task (an import replayed in a second
    session).

    Reads `touch_slug`, the RAW session_tasks relation, not the displayed one:
    E-1967 renders a session bound to the task as `Claimed:` regardless, and
    keying off that would strip a session of the credit for filing the task it
    went on to claim.
    """
    surfaced = [t for t in touches if t["touch_slug"] == "surfaced"]
    if not surfaced:
        return None
    return min(surfaced, key=lambda t: t["first_touch"] or "")


def _session_ref(touch: dict) -> str:
    """A touch's identity as one token pair: 'ES-1020 (E-1865)' — the session and
    the task it is currently active on, or bare 'ES-1020' when it has none."""
    ref = session_id_display(touch["session_id"])
    if touch["task_id"]:
        ref += f" ({task_id_display(touch['task_id'])})"
    return ref


def _session_json(touch: dict) -> dict:
    """A touch as a JSON object for `task show --json` (E-1866). Ids render in
    their display form (ES-NNN / E-NNN) like every other id in that payload.
    `relation` is null for a pre-E-1462 row whose relation was never recorded."""
    return {
        "session": session_id_display(touch["session_id"]),
        "relation": touch["rel_slug"],
        "task": (
            task_id_display(touch["task_id"])
            if touch["task_id"] else None
        ),
        "state": touch["state"],
        "touched_at": touch["last_touch"],
    }


# E-2106: what a `Touched by:` row says when the session's Claude transcript is
# no longer on disk. The row still names a real session that did real work — the
# database row and the worktree survive — but `session goto --resume` /
# `session resume` can no longer open it, so the loss is worth reading while
# reading the task rather than only when a resume refuses.
_GONE_TRANSCRIPT_MARKER = "transcript gone"


def _transcript_gone(touch: dict) -> bool:
    """Does this touch name a session whose transcript is missing from disk?

    False when there is no uuid to look for: a session_tasks row whose sessions
    row is gone has nothing to stat, and "no uuid" is not evidence of loss.

    One glob per row, which is why this is `task show` only and not
    `session list` — a listing pays it per row over the whole table.
    """
    from endless.session_cmd import transcript_path
    uuid = touch.get("uuid")
    if not uuid:
        return False
    return transcript_path(uuid) is None


def _echo_touched_by_section(touches: list[dict], min_width: int = 0) -> bool:
    """Emit the 'Touched by:' session block (E-1866) — the session-side peer of
    'This task:', laid out identically so the two read as siblings: a cyan
    heading, then one '- '-bulleted row per session, '- <Relation>:  ES-NNN
    (E-NNN) [state]', plus '(transcript gone)' when that session can no longer
    be resumed because its Claude transcript has left the disk (E-2106). The relation carries how the task entered that session's
    scope, the ES-NNN id feeds `session goto` directly, and the parenthesized
    task is what the session is bound to now. A session bound to THIS task reads
    `Claimed:` (E-1967) — see _session_touches. Emits nothing and returns False
    when no session ever touched the task."""
    if not touches:
        return False
    width = max(_bullet_label_width(t["rel_label"] for t in touches), min_width)
    finished_states = _finished_session_states()
    click.echo(click.style("Touched by:", fg="cyan"))
    for t in touches:
        color = "green" if t["state"] in finished_states else "yellow"
        label = (t["rel_label"] + ":").ljust(width)
        marker = (
            " " + click.style(f"({_GONE_TRANSCRIPT_MARKER})", fg="red")
            if _transcript_gone(t) else ""
        )
        click.echo(
            f"- {label}{_session_ref(t)} "
            f"[{click.style(t['state'], fg=color)}]{marker}")
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
    if not db.exists("SELECT 1 FROM live_tasks WHERE id = ?", (item_id,)):
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
