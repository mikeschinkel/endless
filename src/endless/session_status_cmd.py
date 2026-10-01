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

from endless import agent_help
from endless import event_bridge
from endless.statuses import TASK_STATUSES
from endless.task_cmd import _current_endless_session_id, _resolve_project


# E-1956: read from the shared vocabulary. This was a fourth hand-maintained
# copy and had drifted to omit `submitted`, so a snapshot naming a submitted
# task was rejected as carrying an invalid status.
_VALID_STATUSES = frozenset(TASK_STATUSES)

# Every refusal in this module below `session_status_add` is a schema
# violation in the XML the agent just composed, so they are all NO-REPORT: the
# agent fixes its own document and runs the command again, and there is nothing
# in that round trip a user would want narrated. Nothing is written until the
# whole document has validated, which is why each summary can say so outright —
# an agent that cannot tell a rejected snapshot from a half-recorded one sends
# the next one twice.
_NOT_RECORDED = "Nothing was recorded."

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
        # A fault, not a validation refusal: the payload passed every check and
        # the event was handed to Go, which answered with nothing. Whether the
        # snapshot reached `session_statuses` is genuinely unknown from here, so
        # the directive that matters is the one `fault` carries — tell the user,
        # and do not retry, because a retry against a write that did land
        # records the same snapshot twice.
        raise agent_help.fault(
            "`endless-go event` returned no output, so whether the snapshot was "
            "recorded is unknown.",
            text="`endless-go event` returned no output; nothing to display.",
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
        raise agent_help.no_report(
            f"session snapshot add: empty input. {_NOT_RECORDED}",
            "Retry piping the snapshot XML on stdin, or passing a file path",
            text="session snapshot add: empty input (expected XML on stdin or "
                 "via file arg)",
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
        raise agent_help.no_report(
            f"session snapshot: malformed XML: {e}. {_NOT_RECORDED}",
            "Fix the XML at the position the parser named and retry",
            text=f"session snapshot: malformed XML: {e}",
        )

    if root.tag != "session-status":
        raise agent_help.no_report(
            f"session snapshot: root element must be <session-status>, got "
            f"<{root.tag}>. {_NOT_RECORDED}",
            "Retry with <session-status> as the root element",
            text=f"session snapshot: root element must be <session-status>, "
                 f"got <{root.tag}>",
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
            raise agent_help.no_report(
                f"session snapshot: unknown element <{tag}> under "
                f"<session-status>. {_NOT_RECORDED}",
                f"Remove or rename <{tag}> and retry",
                text=f"session snapshot: unknown element <{tag}> under "
                     f"<session-status>",
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
            raise agent_help.no_report(
                f"session snapshot: unexpected <{el.tag}> inside <tasks>. "
                f"{_NOT_RECORDED}",
                "Retry with only <task> children under <tasks>",
                text=f"session snapshot: unexpected <{el.tag}> inside <tasks>; "
                     f"only <task> elements allowed",
            )
        tid = el.attrib.get("id", "")
        if not _TASK_ID_RE.match(tid):
            raise agent_help.no_report(
                f"session snapshot: <task id={tid!r}> must match E-NNN. "
                f"{_NOT_RECORDED}",
                "Retry with E-NNN task ids",
                text=f"session snapshot: <task id={tid!r}> must match E-NNN",
            )
        status = el.attrib.get("status", "")
        if status not in _VALID_STATUSES:
            raise agent_help.no_report(
                f"session snapshot: <task id={tid!r}> has invalid status "
                f"{status!r}. {_NOT_RECORDED}",
                "Retry with a status from the listed vocabulary",
                text=f"session snapshot: <task id={tid!r}> has invalid status "
                     f"{status!r}; valid: "
                     f"{', '.join(sorted(_VALID_STATUSES))}",
            )
        filed = el.attrib.get("filed")
        if filed is not None and filed not in ("true", "false"):
            raise agent_help.no_report(
                f"session snapshot: <task id={tid!r}> filed must be 'true' or "
                f"'false', got {filed!r}. {_NOT_RECORDED}",
                "Retry with filed=\"true\" or filed=\"false\"",
                text=f"session snapshot: <task id={tid!r}> filed must be 'true' "
                     f"or 'false', got {filed!r}",
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_summary(section_el: ET.Element) -> str:
    """Serialize <layer> children. Each must have name + files attrs."""
    lines = []
    for el in section_el:
        if el.tag != "layer":
            raise agent_help.no_report(
                f"session snapshot: unexpected <{el.tag}> inside <summary>. "
                f"{_NOT_RECORDED}",
                "Retry with only <layer> children under <summary>",
                text=f"session snapshot: unexpected <{el.tag}> inside "
                     f"<summary>; only <layer> elements allowed",
            )
        if not el.attrib.get("name"):
            raise agent_help.no_report(
                f"session snapshot: <layer> requires a name attribute. "
                f"{_NOT_RECORDED}",
                "Add name= to the <layer> element and retry",
                text="session snapshot: <layer> requires a name attribute",
            )
        if not el.attrib.get("files"):
            raise agent_help.no_report(
                f"session snapshot: <layer> requires a files attribute. "
                f"{_NOT_RECORDED}",
                "Add files= to the <layer> element and retry",
                text="session snapshot: <layer> requires a files attribute",
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_decisions(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "decision":
            raise agent_help.no_report(
                f"session snapshot: unexpected <{el.tag}> inside <decisions>. "
                f"{_NOT_RECORDED}",
                "Retry with only <decision> children under <decisions>",
                text=f"session snapshot: unexpected <{el.tag}> inside "
                     f"<decisions>; only <decision> elements allowed",
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_commits(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "commit":
            raise agent_help.no_report(
                f"session snapshot: unexpected <{el.tag}> inside <commits>. "
                f"{_NOT_RECORDED}",
                "Retry with only <commit> children under <commits>",
                text=f"session snapshot: unexpected <{el.tag}> inside "
                     f"<commits>; only <commit> elements allowed",
            )
        sha = el.attrib.get("sha", "")
        if not _SHA_RE.match(sha):
            raise agent_help.no_report(
                f"session snapshot: <commit sha={sha!r}> must match "
                f"[0-9a-f]{{7,40}}. {_NOT_RECORDED}",
                "Retry with a real 7-40 character hex sha",
                text=f"session snapshot: <commit sha={sha!r}> must match "
                     f"[0-9a-f]{{7,40}}",
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _serialize_memory(section_el: ET.Element) -> str:
    lines = []
    for el in section_el:
        if el.tag != "entry":
            raise agent_help.no_report(
                f"session snapshot: unexpected <{el.tag}> inside <memory>. "
                f"{_NOT_RECORDED}",
                "Retry with only <entry> children under <memory>",
                text=f"session snapshot: unexpected <{el.tag}> inside "
                     f"<memory>; only <entry> elements allowed",
            )
        if not el.attrib.get("path"):
            raise agent_help.no_report(
                f"session snapshot: <entry> requires a path attribute. "
                f"{_NOT_RECORDED}",
                "Add path= to the <entry> element and retry",
                text="session snapshot: <entry> requires a path attribute",
            )
        lines.append(_element_to_line(el))
    return "\n".join(lines)


def _element_to_line(el: ET.Element) -> str:
    """Serialize one element to a single XML line (no leading whitespace)."""
    return ET.tostring(el, encoding="unicode").strip()
