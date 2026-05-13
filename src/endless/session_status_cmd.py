"""CLI implementation for `endless session status add` (E-1312 / E-1314).

Reads XML from a file path or stdin, validates the schema, packages the
parsed contents as a payload, and emits a `session_status.recorded` event
via the existing endless-event bridge. The Go-side handler resolves the
session id from the payload's `process` field, dedups against the latest
row, INSERTs into `session_statuses`, and returns rendered markdown for
chat display.

E-1314 schema: tasks live under a flat `<tasks>` container; disposition
is derived from each task's status at render time (no separate columns).
New `<summary>` element captures structured per-layer implementation
breakdowns.

This module performs no DB access. All persistence lives behind
event_bridge → endless-event → events.Execute per E-894's "DB access in
Go" policy.
"""

import os
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

import click

from endless import event_bridge
from endless.task_cmd import _resolve_project


_VALID_STATUSES = frozenset({
    "needs_plan", "ready", "in_progress", "verify", "confirmed",
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
            "endless-event returned no output; nothing to display."
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
            "session status add: empty input (expected XML on stdin or via file arg)"
        )
    return text


# --- Process identifier ----------------------------------------------------

def _resolve_process(session_id_override: int | None) -> str:
    """Return the process identifier to send to Go.

    With --session-id N, return f"__session_id={N}" as a sentinel that the
    Go side recognizes as 'skip pane lookup, use this id directly.' (Not
    implemented in v1 — override path will be wired when test fixtures
    need it.)

    Otherwise return TMUX_PANE env var. Go validates and errors clearly
    if the value doesn't resolve to a live session.
    """
    if session_id_override is not None:
        # Reserved for future override wiring; for v1 just error so the
        # caller knows this path isn't supported yet.
        raise click.ClickException(
            "--session-id override is reserved for tests; not implemented in v1"
        )
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
        raise click.ClickException(f"session status: malformed XML: {e}")

    if root.tag != "session-status":
        raise click.ClickException(
            f"session status: root element must be <session-status>, got <{root.tag}>"
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
                f"session status: unknown element <{tag}> under <session-status>"
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
                f"session status: unexpected <{el.tag}> inside <tasks>; "
                f"only <task> elements allowed"
            )
        tid = el.attrib.get("id", "")
        if not _TASK_ID_RE.match(tid):
            raise click.ClickException(
                f"session status: <task id={tid!r}> must match E-NNN"
            )
        status = el.attrib.get("status", "")
        if status not in _VALID_STATUSES:
            raise click.ClickException(
                f"session status: <task id={tid!r}> has invalid status "
                f"{status!r}; valid: {', '.join(sorted(_VALID_STATUSES))}"
            )
        filed = el.attrib.get("filed")
        if filed is not None and filed not in ("true", "false"):
            raise click.ClickException(
                f"session status: <task id={tid!r}> filed must be 'true' "
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
                f"session status: unexpected <{el.tag}> inside <summary>; "
                f"only <layer> elements allowed"
            )
        if not el.attrib.get("name"):
            raise click.ClickException(
                "session status: <layer> requires a name attribute"
            )
        if not el.attrib.get("files"):
            raise click.ClickException(
                "session status: <layer> requires a files attribute"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_decisions(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "decision":
            raise click.ClickException(
                f"session status: unexpected <{el.tag}> inside <decisions>; "
                f"only <decision> elements allowed"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_commits(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "commit":
            raise click.ClickException(
                f"session status: unexpected <{el.tag}> inside <commits>; "
                f"only <commit> elements allowed"
            )
        sha = el.attrib.get("sha", "")
        if not _SHA_RE.match(sha):
            raise click.ClickException(
                f"session status: <commit sha={sha!r}> must match [0-9a-f]{{7,40}}"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_memory(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "entry":
            raise click.ClickException(
                f"session status: unexpected <{el.tag}> inside <memory>; "
                f"only <entry> elements allowed"
            )
        if not el.attrib.get("path"):
            raise click.ClickException(
                "session status: <entry> requires a path attribute"
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _element_to_line(el: ET.Element) -> str:
    """Serialize one element to a single XML line (no leading whitespace)."""
    return ET.tostring(el, encoding="unicode").strip()
