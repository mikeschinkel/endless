"""CLI implementation for `endless session order` (E-1683).

Sets a per-session implementation order on the tasks this session has
touched, so a later `session status` can show the do/plan group in the
sequence to work them, with parallel groups. The order is stored as
`session_tasks.do_order` (session-scoped, distinct from the global
`tasks.sort_order`); equal values mark tasks parallelizable.

Input is a compact spec where whitespace advances the order counter and
`|` groups tasks at the SAME order (parallel):

    E-100 E-101|E-102 E-103   ->  E-100=1, E-101=2, E-102=2, E-103=3

`--json` instead parses SPEC as an array-of-groups, isomorphic to the
compact form: ``[["E-100"], ["E-101", "E-102"], ["E-103"]]``.

This module performs no DB access. Persistence is replace-all and lives
behind event_bridge -> endless-go event -> events.Execute per the
"DB access in Go" policy: the parsed groups are emitted as a
`session_tasks.ordered` event whose Go handler validates that every id is
already a session_tasks row for this session and rewrites do_order.
"""

import json
import os
import re

import click

from endless import event_bridge
from endless.task_cmd import _current_endless_session_id, _resolve_project


_TASK_ID_RE = re.compile(r"^[Ee]-(\d+)$")


def session_order(spec: str, as_json: bool, session_id_override: int | None) -> None:
    """Entry point bound by cli.py. Parse SPEC, emit, print the result."""
    groups = _parse_json_spec(spec) if as_json else _parse_compact_spec(spec)

    process = _resolve_process(session_id_override)
    _project_id, project_name = _resolve_project(None)
    # An explicit --session-id also names the actor, so attribution is settled
    # without the resolver (lets a non-tmux caller / test fixture emit). When
    # absent, emit_event resolves actor.session_id the same way _resolve_process
    # resolved the sentinel.
    session_id = str(session_id_override) if session_id_override is not None else None
    result = event_bridge.emit_event(
        kind="session_tasks.ordered",
        project=project_name,
        entity_type="session_tasks",
        # Rows aren't pre-allocated and the session is resolved from `process`
        # Go-side; a placeholder entity_id keeps the generic emit path happy.
        entity_id="0",
        payload={"process": process, "groups": groups},
        session_id=session_id,
    )

    if result is None:
        raise click.ClickException(
            "`endless-go event` returned no output; nothing to display."
        )

    markdown = result.get("markdown", "")
    if markdown:
        click.echo(markdown)


# --- Process identifier ----------------------------------------------------

def _resolve_process(session_id_override: int | None) -> str:
    """Return the process identifier to send to Go (mirrors session status).

    Resolves the Endless session id and returns the reserved sentinel
    `f"__session_id={N}"`, which the Go side recognizes as "use this id
    directly." Falls back to the raw TMUX_PANE so Go's pane lookup runs and
    emits its clear "no live session for process" error when no id is known.
    """
    if session_id_override is not None:
        return f"__session_id={session_id_override}"
    eid = _current_endless_session_id()
    if eid is not None:
        return f"__session_id={eid}"
    return os.environ.get("TMUX_PANE", "")


# --- Spec parsing ----------------------------------------------------------

def _parse_compact_spec(spec: str) -> list[list[str]]:
    """Parse the compact spec into an ordered list of parallel groups.

    Whitespace separates orders; `|` separates parallel ids within an order.
    Raises a ClickException on an empty spec/group or a malformed/duplicate id.
    """
    spec = spec.strip()
    if not spec:
        raise click.ClickException("session order: empty spec")

    seen: set[str] = set()
    groups: list[list[str]] = []
    for token in spec.split():
        group: list[str] = []
        for raw in token.split("|"):
            group.append(_canonical_id(raw, seen))
        groups.append(group)
    return groups


def _parse_json_spec(spec: str) -> list[list[str]]:
    """Parse SPEC as a JSON array-of-groups (each inner array a parallel group)."""
    try:
        data = json.loads(spec)
    except json.JSONDecodeError as e:
        raise click.ClickException(f"session order --json: malformed JSON: {e}")

    if not isinstance(data, list) or not data:
        raise click.ClickException(
            "session order --json: expected a non-empty array of groups, "
            'e.g. [["E-100"], ["E-101", "E-102"]]'
        )

    seen: set[str] = set()
    groups: list[list[str]] = []
    for grp in data:
        if not isinstance(grp, list) or not grp:
            raise click.ClickException(
                "session order --json: each group must be a non-empty array of "
                "task ids"
            )
        group: list[str] = []
        for raw in grp:
            if not isinstance(raw, str):
                raise click.ClickException(
                    f"session order --json: task id must be a string, got {raw!r}"
                )
            group.append(_canonical_id(raw, seen))
        groups.append(group)
    return groups


def _canonical_id(raw: str, seen: set[str]) -> str:
    """Validate one id, normalize to `E-NNN`, and reject duplicates."""
    m = _TASK_ID_RE.match(raw.strip())
    if not m:
        raise click.ClickException(
            f"session order: malformed task id {raw!r} (expected E-NNN)"
        )
    cid = f"E-{m.group(1)}"
    if cid in seen:
        raise click.ClickException(
            f"session order: task {cid} listed more than once"
        )
    seen.add(cid)
    return cid
