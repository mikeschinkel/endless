"""CLI implementation for `endless session touch` (E-2173, E-2291), its
aliases `endless touch` and `endless session task add`, and `endless session
task remove` (E-1696).

Two verbs, and an alias, that say explicitly what a session's task list
holds.
`session_tasks` capture is otherwise automatic — the Go executors record a row
for every task a session claims, files or edits — and these cover what
automation cannot reach or gets wrong.

    session touch <ids>        enroll at relation `touched`: the user's
                               command for putting a task on the session's
                               `session status` list without changing it, so
                               no automatic capture would ever record it.
                               `touch` and `session task add` are aliases
                               (E-2291): same event, same relation.

    session task remove <ids>  drop the association entirely, for a capture
                               that should not have happened.

`remove` is deliberately NOT the inverse of `session hide --task` (E-1914).
Hide suppresses a row from one session's `session status` listing while
KEEPING the association, so `task show`'s "Sessions:" still reports the
touch that really happened — it is for a capture that is real but noisy.
Remove deletes the row: the touch and its relation. It also clears any hide on
the same pair, so a later re-capture does not come back silently suppressed.

This module performs no DB access. All three verbs emit an event
(`session_tasks.touched` / `session_tasks.removed`)
via event_bridge -> endless-go event -> events.Execute, per the "DB access in
Go" policy. The Go executors own validation: unknown task ids are rejected
there, the upgrade-only relation ladder decides what `touch` actually stores,
and `remove` refuses the session's own claimed or bound task.
"""

import os
import re

import click

from endless import agent_help, event_bridge
from endless.task_cmd import _current_endless_session_id, _resolve_project


_TASK_ID_RE = re.compile(r"^[Ee]-(\d+)$")

# Event kind -> what the user typed, so a malformed-id refusal names the command
# that produced it rather than the module it landed in. The two `session task`
# verbs share one label: the group is what a reader recognizes, and the
# complaint is about the ids, which both parse identically.
_VERB_OF = {
    "session_tasks.removed": "session task",
    "session_tasks.touched": "touch",
    "session_tasks.hidden": "session hide",
    "session_tasks.unhidden": "session unhide",
}


def session_task_add(task_refs: tuple[str, ...],
                     session_id_override: int | None) -> None:
    """Entry point bound by cli.py for `session task add` — an alias of
    `session touch` since E-2291, emitting the same event."""
    touch(task_refs, session_id_override)


def session_task_remove(task_refs: tuple[str, ...],
                        session_id_override: int | None) -> None:
    """Entry point bound by cli.py for `session task remove`."""
    emit_membership("session_tasks.removed", task_refs, session_id_override)


def touch(task_refs: tuple[str, ...],
          session_id_override: int | None) -> None:
    """Entry point bound by cli.py for `session touch` and its alias `touch`
    (E-2173, E-2291).

    Records relation `touched`: the task is in this session's scope, put
    there on purpose, with nothing about the task itself changed.
    """
    emit_membership("session_tasks.touched", task_refs, session_id_override)


def emit_membership(kind: str, task_refs: tuple[str, ...],
                    session_id_override: int | None) -> None:
    """Normalize the ids, emit `kind`, and print the Go handler's markdown.

    Shared with `session hide/unhide --task` (E-2293), whose input is the same
    (one session, a set of tasks) and whose writes moved to Go alongside these.
    """
    task_ids = _canonical_ids(task_refs, _VERB_OF[kind])

    process = _resolve_process(session_id_override)
    _project_id, project_name = _resolve_project(None)
    # An explicit --session-id also names the actor, so attribution is settled
    # without the resolver (lets a non-tmux caller / test fixture emit). When
    # absent, emit_event resolves actor.session_id the same way _resolve_process
    # resolved the sentinel.
    session_id = str(session_id_override) if session_id_override is not None else None
    result = event_bridge.emit_event(
        kind=kind,
        project=project_name,
        entity_type="session_tasks",
        # Rows aren't pre-allocated and the session is resolved from `process`
        # Go-side; a placeholder entity_id keeps the generic emit path happy.
        entity_id="0",
        payload={"process": process, "task_ids": task_ids},
        session_id=session_id,
    )

    if result is None:
        # The TSV says class REPORT, kind fault, and `fault` is how that pair
        # is rendered: its directive already says tell the user and do not
        # retry. The retry prohibition is the load-bearing part here. A silent
        # emit is not a refusal — the event may well have been applied and only
        # its rendering lost — so an agent that treats the silence as "it did
        # not happen" and runs the verb again can enroll the same tasks twice.
        # The summary says that unknown outcome out loud for exactly that
        # reason; the human's line is unchanged.
        raise agent_help.fault(
            f"`endless-go event` returned no output for {kind}, so whether the "
            "event was applied is unknown — do not re-run it.",
            text="`endless-go event` returned no output; nothing to display.",
        )

    markdown = result.get("markdown", "")
    if markdown:
        click.echo(markdown.rstrip("\n"))


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


def _canonical_ids(task_refs: tuple[str, ...], verb: str) -> list[str]:
    """Validate each id and normalize to `E-NNN`, preserving argument order.

    A repeated id is collapsed rather than rejected: naming the same task twice
    is the same request, not a contradiction.

    `verb` prefixes both refusals so they name the command the user typed.
    """
    if not task_refs:
        # Both refusals here are the TSV's NO-REPORT usage/validation rows, and
        # both run before `emit_membership`, so no event exists to have half-landed — the
        # one-line message is the whole verdict and goes through verbatim.
        raise agent_help.no_report(
            f"{verb}: name at least one task id",
            "Retry naming one or more E-NNN task ids")

    seen: set[str] = set()
    out: list[str] = []
    for raw in task_refs:
        m = _TASK_ID_RE.match(raw.strip())
        if not m:
            raise agent_help.no_report(
                f"{verb}: malformed task id {raw!r} (expected E-NNN)",
                "Retry with E-NNN ids")
        cid = f"E-{m.group(1)}"
        if cid not in seen:
            seen.add(cid)
            out.append(cid)
    return out
