"""CLI implementation for `endless session task add|remove` (E-1696).

Two verbs that correct what a session's task list holds. `session_tasks`
capture is otherwise automatic — the Go executors record a row for every
task a session claims, files or edits — and these are the manual overrides
for the two cases automation gets wrong.

    session task add <ids>     promote to relation `queued`: decided session
                               work the session has not touched yet, so no
                               automatic capture would ever record it.

    session task remove <ids>  drop the association entirely, for a capture
                               that should not have happened.

`remove` is deliberately NOT the inverse of `session hide --task` (E-1914).
Hide suppresses a row from one session's `session status` listing while
KEEPING the association, so `task show`'s "Touched by:" still reports the
touch that really happened — it is for a capture that is real but noisy.
Remove deletes the row: the touch, its relation and its do_order. It also
clears any hide on the same pair, so a later re-capture does not come back
silently suppressed.

This module performs no DB access. Both verbs emit an event
(`session_tasks.queued` / `session_tasks.removed`) via event_bridge ->
endless-go event -> events.Execute, per the "DB access in Go" policy. The
Go executors own validation: unknown task ids are rejected there, the
upgrade-only relation ladder decides what `add` actually stores, and
`remove` refuses the session's own goal task.
"""

import os
import re

import click

from endless import event_bridge
from endless.task_cmd import _current_endless_session_id, _resolve_project


_TASK_ID_RE = re.compile(r"^[Ee]-(\d+)$")


def session_task_add(task_refs: tuple[str, ...],
                     session_id_override: int | None) -> None:
    """Entry point bound by cli.py for `session task add`."""
    _emit("session_tasks.queued", task_refs, session_id_override)


def session_task_remove(task_refs: tuple[str, ...],
                        session_id_override: int | None) -> None:
    """Entry point bound by cli.py for `session task remove`."""
    _emit("session_tasks.removed", task_refs, session_id_override)


def _emit(kind: str, task_refs: tuple[str, ...],
          session_id_override: int | None) -> None:
    """Normalize the ids, emit `kind`, and print the Go handler's markdown."""
    task_ids = _canonical_ids(task_refs)

    process = _resolve_process(session_id_override)
    _project_id, project_name = _resolve_project(None)
    # An explicit --session-id also names the actor, so attribution is settled
    # without the resolver (lets a non-tmux caller / test fixture emit). When
    # absent, emit_event resolves actor.session_id the same way _resolve_process
    # resolved the sentinel. Mirrors session_order_cmd.
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
        raise click.ClickException(
            "`endless-go event` returned no output; nothing to display."
        )

    markdown = result.get("markdown", "")
    if markdown:
        click.echo(markdown.rstrip("\n"))


def _resolve_process(session_id_override: int | None) -> str:
    """Return the process identifier to send to Go (mirrors session order).

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


def _canonical_ids(task_refs: tuple[str, ...]) -> list[str]:
    """Validate each id and normalize to `E-NNN`, preserving argument order.

    A repeated id is collapsed rather than rejected: naming the same task twice
    is the same request, not a contradiction (unlike `session order`, where a
    duplicate is genuinely ambiguous about which position it wants).
    """
    if not task_refs:
        raise click.ClickException("session task: name at least one task id")

    seen: set[str] = set()
    out: list[str] = []
    for raw in task_refs:
        m = _TASK_ID_RE.match(raw.strip())
        if not m:
            raise click.ClickException(
                f"session task: malformed task id {raw!r} (expected E-NNN)"
            )
        cid = f"E-{m.group(1)}"
        if cid not in seen:
            seen.add(cid)
            out.append(cid)
    return out
