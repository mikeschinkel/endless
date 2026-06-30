"""CLI implementation for `endless session snapshot add` (E-1312 / E-1314).

Reads XML from a file path or stdin, validates the schema, packages the
parsed contents as a payload, and emits a `session_status.recorded` event
via the existing `endless-go event` bridge. The Go-side handler resolves the
session id from the payload's `process` field, dedups against the latest
row, INSERTs into `session_statuses`, and returns rendered markdown for
chat display.

E-1314 schema: tasks live under a flat `<tasks>` container; disposition
is derived from each task's status at render time (no separate columns).
New `<summary>` element captures structured per-layer implementation
breakdowns.

This module performs no DB access. All persistence lives behind
event_bridge → endless-go event → events.Execute per E-894's "DB access in
Go" policy.
"""

import os
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

import click

from endless import event_bridge
from endless.task_cmd import _current_endless_session_id, _resolve_project


_VALID_STATUSES = frozenset({
    "unplanned", "ready", "underway", "unverified", "confirmed",
    "assumed", "completed", "blocked", "revisit", "declined", "obsolete",
})

_TASK_ID_RE = re.compile(r"^E-\d+$")
_SHA_RE = re.compile(r"^[0-9a-f]{7,40}$")


def session_status_add(input_file: str | None, session_id_override: int | None) -> None:
    """Entry point bound by cli.py.

    Reads input from `input_file` if given, otherwise stdin. Parses,
    validates, emits, and prints the resulting markdown.
    """
    xml_text = _read_input(input_file)
    payload = _parse_and_validate(xml_text)
    payload["process"] = _resolve_process(session_id_override)

    # event_bridge.emit_event expects an entity_type/entity_id; use a
    # placeholder entity_id since session_status rows aren't pre-allocated.
    project_id, project_name = _resolve_project(None)
    result = event_bridge.emit_event(
        kind="session_status.recorded",
        project=project_name,
        entity_type="session_status",
        entity_id="0",
        payload=payload,
    )

    if result is None:
        raise click.ClickException(
            "`endless-go event` returned no output; nothing to display."
        )

    markdown = result.get("markdown", "")
    if markdown:
        click.echo(markdown)

    if result.get("skipped"):
        click.echo(
            click.style("•", fg="yellow")
            + " skipped: identical to latest status for this session"
        )
    elif result.get("session_status_id"):
        sid = result["session_status_id"]
        click.echo(
            click.style("•", fg="green")
            + f" recorded as session_statuses.id={sid}"
        )


# --- Input -----------------------------------------------------------------

def _read_input(input_file: str | None) -> str:
    """Return the XML text from a file path or stdin."""
    if input_file:
        text = Path(input_file).read_text()
    else:
        text = sys.stdin.read()
    if not text.strip():
        raise click.ClickException(
            "session snapshot add: empty input (expected XML on stdin or via file arg)"
        )
    return text


# --- Process identifier ----------------------------------------------------

def _resolve_process(session_id_override: int | None) -> str:
    """Return the process identifier to send to Go (E-1588).

    Resolves the Endless session id and returns the reserved sentinel
    `f"__session_id={N}"`, which the Go side recognizes as "skip the
    tmux-pane lookup, use this id directly." Two ways an id becomes known:

    1. An explicit `--session-id N` override (test fixtures, non-tmux
       callers).
    2. The unified `_current_endless_session_id()` resolver, which covers
       the CLAUDECODE-env tier and the E-1585 `@endless_session_uuid`
       window-option tier — so a fresh `--db sandbox` works in a worktree
       and a sibling shell pane resolves to its window's Claude session.
       This entry point is heuristic-free (no prompting), preserving the
       command's non-interactive behavior.

    When neither yields an id, fall back to the raw TMUX_PANE so Go's
    pane lookup still runs and emits its clear "no live session for
    process" error.
    """
    if session_id_override is not None:
        return f"__session_id={session_id_override}"
    eid = _current_endless_session_id()
    if eid is not None:
        return f"__session_id={eid}"
    return os.environ.get("TMUX_PANE", "")


# --- XML parse + validate --------------------------------------------------

def _parse_and_validate(xml_text: str) -> dict:
    """Parse the input XML; validate against the E-1312 schema.

    Returns a payload dict mapping each section to its serialized XML
    contents (task sections) or text content (headline/notes). Missing
    sections map to empty strings, not None.
    """
    try:
        root = ET.fromstring(xml_text)
    except ET.ParseError as e:
        raise click.ClickException(f"session snapshot: malformed XML: {e}")

    if root.tag != "session-status":
        raise click.ClickException(
            f"session snapshot: root element must be <session-status>, got <{root.tag}>"
        )

    payload = {
        "headline": "",
        "tasks": "",
        "decisions": "",
        "commits": "",
        "memory": "",
        "summary": "",
        "notes": "",
    }

    for child in root:
        tag = child.tag
        if tag == "headline":
            payload["headline"] = (child.text or "").strip()
        elif tag == "notes":
            payload["notes"] = (child.text or "").strip()
        elif tag == "tasks":
            payload["tasks"] = _serialize_tasks(child)
        elif tag == "decisions":
            payload["decisions"] = _serialize_decisions(child)
        elif tag == "commits":
            payload["commits"] = _serialize_commits(child)
        elif tag == "memory":
            payload["memory"] = _serialize_memory(child)
        elif tag == "summary":
            payload["summary"] = _serialize_summary(child)
        else:
            raise click.ClickException(
                f"session snapshot: unknown element <{tag}> under <session-status>"
            )

    return payload


def _serialize_tasks(section_el: ET.Element) -> str:
    """Serialize each <task> child as one line; validate attrs.

    E-1314: tasks live under a single flat <tasks> container; disposition
    is derived from `status` at render time. Schema validates `id`,
    `status`, and the optional `filed` attribute.
    """
    lines = []
    for el in section_el:
        if el.tag != "task":
            raise click.ClickException(
                f"session snapshot: unexpected <{el.tag}> inside <tasks>; "
                f"only <task> elements allowed"
            )
        tid = el.attrib.get("id", "")
        if not _TASK_ID_RE.match(tid):
            raise click.ClickException(
                f"session snapshot: <task id={tid!r}> must match E-NNN"
            )
        status = el.attrib.get("status", "")
        if status not in _VALID_STATUSES:
            raise click.ClickException(
                f"session snapshot: <task id={tid!r}> has invalid status "
                f"{status!r}; valid: {', '.join(sorted(_VALID_STATUSES))}"
            )
        filed = el.attrib.get("filed")
        if filed is not None and filed not in ("true", "false"):
            raise click.ClickException(
                f"session snapshot: <task id={tid!r}> filed must be 'true' "
                f"or 'false', got {filed!r}"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_summary(section_el: ET.Element) -> str:
    """Serialize <layer> children. Each must have name + files attrs."""
    lines = []
    for el in section_el:
        if el.tag != "layer":
            raise click.ClickException(
                f"session snapshot: unexpected <{el.tag}> inside <summary>; "
                f"only <layer> elements allowed"
            )
        if not el.attrib.get("name"):
            raise click.ClickException(
                "session snapshot: <layer> requires a name attribute"
            )
        if not el.attrib.get("files"):
            raise click.ClickException(
                "session snapshot: <layer> requires a files attribute"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_decisions(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "decision":
            raise click.ClickException(
                f"session snapshot: unexpected <{el.tag}> inside <decisions>; "
                f"only <decision> elements allowed"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_commits(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "commit":
            raise click.ClickException(
                f"session snapshot: unexpected <{el.tag}> inside <commits>; "
                f"only <commit> elements allowed"
            )
        sha = el.attrib.get("sha", "")
        if not _SHA_RE.match(sha):
            raise click.ClickException(
                f"session snapshot: <commit sha={sha!r}> must match [0-9a-f]{{7,40}}"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_memory(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "entry":
            raise click.ClickException(
                f"session snapshot: unexpected <{el.tag}> inside <memory>; "
                f"only <entry> elements allowed"
            )
        if not el.attrib.get("path"):
            raise click.ClickException(
                "session snapshot: <entry> requires a path attribute"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _element_to_line(el: ET.Element) -> str:
    """Serialize one element to a single XML line (no leading whitespace)."""
    return ET.tostring(el, encoding="unicode").strip()
