"""`endless task report <id>` — the steering-prompt reporting command (E-1771).

End-of-session reports stop being freeform prose the agent composes. Instead the
agent runs this command; the command computes the facts it can (status,
successors, children, worktree state), gates the one free-text escape hatch
(notes/questions) with a per-entry Haiku check, and prints a *steering prompt*
that tells the agent to relay only those facts to the user — plainly, no
ceremony. This revises ED-1531 (the output steers the agent rather than being
relayed verbatim); persisting the facts as queryable rows is E-1777.

The normal path is `endless task report <id>` with NO payload: zero free-text
entries means zero Haiku calls, and the command steers on computed facts alone.
"""

import json
import subprocess

import click

from endless import config, internal_claude, report_prompts

_NOTE_KINDS = ("anomaly", "discovery")
_QUESTION_TYPES = ("text", "integer", "real", "boolean", "choice")


# --- payload parsing --------------------------------------------------------

def _parse_payload(payload: str | None) -> tuple[list[dict], list[dict]]:
    """Parse and validate the agent's `--json` payload into (notes, questions).

    Empty/omitted payload is the normal path → ([], []). Rejection is
    verb-check-style: a `click.ClickException` on malformed JSON, unknown keys,
    or an out-of-set `kind`/`type`. The agent supplies genuinely non-computable
    facts and open decisions; nothing else has a place to go.
    """
    if payload is None or not payload.strip():
        return [], []
    try:
        data = json.loads(payload)
    except json.JSONDecodeError as e:
        raise click.ClickException(f"--json payload is not valid JSON: {e}")
    if not isinstance(data, dict):
        raise click.ClickException(
            'report payload must be a JSON object, e.g. {"notes": [...], "questions": [...]}'
        )
    unknown = set(data) - {"notes", "questions"}
    if unknown:
        raise click.ClickException(
            f"unknown report field(s): {', '.join(sorted(unknown))}. "
            "Only 'notes' and 'questions' are accepted."
        )
    notes = _parse_notes(data.get("notes", []))
    questions = _parse_questions(data.get("questions", []))
    return notes, questions


def _parse_notes(raw) -> list[dict]:
    if not isinstance(raw, list):
        raise click.ClickException("'notes' must be a list.")
    out = []
    for i, n in enumerate(raw):
        if not isinstance(n, dict):
            raise click.ClickException(f"note #{i + 1} must be an object.")
        unknown = set(n) - {"kind", "text"}
        if unknown:
            raise click.ClickException(
                f"note #{i + 1} has unknown field(s): {', '.join(sorted(unknown))}."
            )
        kind = n.get("kind")
        text = n.get("text")
        if kind not in _NOTE_KINDS:
            raise click.ClickException(
                f"note #{i + 1} kind must be one of {', '.join(_NOTE_KINDS)}; got {kind!r}."
            )
        if not isinstance(text, str) or not text.strip():
            raise click.ClickException(f"note #{i + 1} needs non-empty 'text'.")
        out.append({"kind": kind, "text": text.strip()})
    return out


def _parse_questions(raw) -> list[dict]:
    if not isinstance(raw, list):
        raise click.ClickException("'questions' must be a list.")
    out = []
    for i, q in enumerate(raw):
        if not isinstance(q, dict):
            raise click.ClickException(f"question #{i + 1} must be an object.")
        unknown = set(q) - {"text", "type", "style"}
        if unknown:
            raise click.ClickException(
                f"question #{i + 1} has unknown field(s): {', '.join(sorted(unknown))}."
            )
        text = q.get("text")
        qtype = q.get("type", "text")
        style = q.get("style")
        if not isinstance(text, str) or not text.strip():
            raise click.ClickException(f"question #{i + 1} needs non-empty 'text'.")
        if qtype not in _QUESTION_TYPES:
            raise click.ClickException(
                f"question #{i + 1} type must be one of {', '.join(_QUESTION_TYPES)}; got {qtype!r}."
            )
        entry = {"text": text.strip(), "type": qtype}
        if isinstance(style, str) and style.strip():
            entry["style"] = style.strip()
        out.append(entry)
    return out


# --- per-entry Haiku gate ---------------------------------------------------

def _classify(prompt_text: str, entry_text: str) -> tuple[bool, str | None]:
    """Ask Haiku to KEEP or DROP one free-text entry.

    Returns (keep, reason). Fail-open: any failure (timeout, missing binary,
    non-zero exit, unparseable reply) returns (True, None) so an infra outage
    can never block a genuine handoff — the gate is a best-effort quality
    filter, not a hard blocker (diverges from the fail-closed verb-gate because
    a false block here is far costlier).
    """
    prompt = prompt_text.format(text=entry_text)
    try:
        result = internal_claude.run_internal_claude(prompt, model="haiku", timeout=30)
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return True, None
    if result.returncode != 0:
        return True, None
    response = result.stdout.strip()
    upper = response.upper()
    if upper.startswith("KEEP"):
        return True, None
    if upper.startswith("DROP"):
        reason = response[len("DROP"):].lstrip(": ").strip()
        return False, (reason or None)
    # Unparseable → fail-open.
    return True, None


def _running_under_agent() -> bool:
    from endless.task_cmd import _running_under_agent as impl
    return impl()


def _bounce_note(text: str, reason: str | None) -> None:
    detail = f"\n  Check: {reason}" if reason else ""
    if _running_under_agent():
        raise click.ClickException(
            f"This note reads as ceremony, not a genuine non-computable fact:\n"
            f'    "{text}"{detail}\n'
            f"\n"
            f"  A handoff note must state something the reviewer could NOT compute\n"
            f"  from git or the task tracker. Remove it, or replace it with the\n"
            f"  actual out-of-band fact. Do not reword ceremony to pass this check."
        )
    raise click.ClickException(
        f'This note reads as ceremony rather than a real anomaly:\n    "{text}"{detail}\n'
        f"  Drop it or replace it with the genuine out-of-band fact."
    )


def _bounce_question(text: str, reason: str | None) -> None:
    detail = f"\n  Check: {reason}" if reason else ""
    if _running_under_agent():
        raise click.ClickException(
            f"This question restates settled state rather than asking for a real decision:\n"
            f'    "{text}"{detail}\n'
            f"\n"
            f"  A handoff question must be a decision the user has to make before\n"
            f"  the work can proceed. Remove it, or replace it with the actual open\n"
            f"  decision. Do not invent a decision to pass this check."
        )
    raise click.ClickException(
        f'This question restates settled state rather than asking a real decision:\n    "{text}"{detail}\n'
        f"  Drop it or replace it with the genuine open decision."
    )


def _gate(notes: list[dict], questions: list[dict], prompts: dict[str, str]) -> None:
    """Bounce any ceremonial note/question. Fires only when entries exist."""
    for n in notes:
        keep, reason = _classify(prompts[report_prompts.NOTE_CHECK], n["text"])
        if not keep:
            _bounce_note(n["text"], reason)
    for q in questions:
        keep, reason = _classify(prompts[report_prompts.QUESTION_CHECK], q["text"])
        if not keep:
            _bounce_question(q["text"], reason)


# --- computed facts ---------------------------------------------------------

def _compute_facts(item_id: int) -> dict:
    """Fetch the computed report facts from Go (status, landed, successors, children)."""
    from endless.event_bridge import _resolve_endless_go
    go_bin = _resolve_endless_go()
    config.require_db_context()
    try:
        result = subprocess.run(
            [go_bin, *config.go_db_context_args(),
             "session-query", "task-report", "--id", str(item_id)],
            capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError) as e:
        raise click.ClickException(f"endless-go failed: {e}")
    if result.returncode != 0:
        raise click.ClickException(result.stderr.strip() or "endless-go failed")
    try:
        return json.loads(result.stdout)
    except ValueError:
        raise click.ClickException("endless-go returned malformed report facts")


def _compute_anomalies() -> list[str]:
    """Genuine git/worktree anomaly lines for the current worktree, or [].

    Reuses the DB-free `session-query worktree-anomalies` probe. When cwd is not
    inside an endless worktree there is nothing to inspect → []. Anomalies are an
    AGENT concern; the steering prompt tells the agent to surface them to the
    user only when unexpected.
    """
    import shutil
    from endless.worktree_cmd import worktree_root_for_cwd
    root = worktree_root_for_cwd()
    if root is None:
        return []
    binary = shutil.which("endless-go")
    if not binary:
        return []
    main_root = str(root.parents[2])
    try:
        result = subprocess.run(
            [binary, "session-query", "worktree-anomalies",
             "--worktree-path", str(root), "--project-root", main_root],
            capture_output=True, text=True, timeout=10,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return []
    return [ln for ln in result.stdout.splitlines() if ln.strip()]


# --- render -----------------------------------------------------------------

def _ref_line(refs: list[dict]) -> str:
    return ", ".join(f"E-{r['id']} [{r['status']}]" for r in refs)


def _render_facts(item_id: int, facts: dict, anomalies: list[str],
                  notes: list[dict], questions: list[dict]) -> str:
    """Assemble the fact block. Empty categories are omitted (terse for the
    human); only non-empty sections appear."""
    lines: list[str] = []
    lines.append(f"Task: E-{item_id}")
    lines.append(f"Status: {facts.get('status', '')}")
    if facts.get("landed"):
        lines.append("Landed: yes")

    successors = facts.get("successors") or []
    if successors:
        lines.append(f"Follow-ups you filed: {_ref_line(successors)}")

    children = facts.get("children") or []
    if children:
        lines.append(f"Children: {_ref_line(children)}")

    if notes:
        lines.append("Notes to relay:")
        for n in notes:
            lines.append(f"  - [{n['kind']}] {n['text']}")

    if questions:
        lines.append("Questions for the user:")
        for q in questions:
            style = f"/{q['style']}" if q.get("style") else ""
            lines.append(f"  - {q['text']}  ({q['type']}{style})")

    if anomalies:
        lines.append(
            "Uncommitted/worktree state (for you — surface to the user ONLY if "
            "unexpected, e.g. a change you did not make):"
        )
        for a in anomalies:
            lines.append(f"  - {a}")

    return "\n".join(lines)


def report_item(item_id: int, payload: str | None) -> None:
    """Compute facts, gate the free-text payload, and print the steering prompt."""
    notes, questions = _parse_payload(payload)
    prompts = report_prompts.load_prompts()
    _gate(notes, questions, prompts)  # raises on a bounced entry
    facts = _compute_facts(item_id)
    anomalies = _compute_anomalies()
    fact_block = _render_facts(item_id, facts, anomalies, notes, questions)
    click.echo(prompts[report_prompts.STEER].format(facts=fact_block))
