"""Per-session activity reporting from the event ledger (E-1285).

`endless session activity` reads `.endless/db-ledger/*.jsonl`, filters
events by `actor.session_id` (populated since E-1284/E-1287/E-1294),
groups by event kind, and renders the archetype-2 shape report:

    Session ES-356 — endless — started 2026-05-12T14:23:12

      Filed (3): E-1278, E-1279, E-1280
      Claimed (5): E-1284, E-1287, E-1294 ...
      Shipped → verify (4): ...
      Confirmed (8): ...

No SQL schema is added — this is a pure projection over the ledger,
which is the source of truth for events.
"""

from __future__ import annotations

import json
from pathlib import Path

import click

from endless import db


# Status values that have their own grouping line in the rendered report.
# Other statuses (in_progress, ready, etc.) fold into the catch-all.
_REPORTED_STATUSES: tuple[str, ...] = (
    "verify", "confirmed", "assumed", "declined", "obsolete",
)

_REPORTED_STATUS_LABELS: dict[str, str] = {
    "verify": "Shipped → verify",
    "confirmed": "Confirmed",
    "assumed": "Assumed",
    "declined": "Declined",
    "obsolete": "Obsoleted",
}


def session_activity(
    session_ref: str | None = None,
    kinds_filter: list[str] | None = None,
    as_json: bool = False,
    pane: str | None = None,
) -> None:
    """Entry point for the `endless session activity` CLI verb.

    When `pane` is given, it overrides $TMUX_PANE for the duration of
    session resolution. Required when the command runs inside a
    `tmux display-popup` (the popup has its own pane id which doesn't
    match any Claude session); the menu binding passes #{pane_id} of
    the focused pane at menu-invocation time. Mirrors the same trick
    used by `endless-go tmux status-line --pane=...` (E-1236).
    """
    session_id = _resolve_session_id(session_ref, pane_override=pane)
    project_root = _project_root_for_cwd()
    events = _read_session_events(project_root, str(session_id))

    if kinds_filter:
        events = [e for e in events if e.get("kind") in kinds_filter]

    if as_json:
        click.echo(json.dumps({
            "session_id": session_id,
            "event_count": len(events),
            "events": events,
        }, indent=2))
        return

    _render(session_id, events)


def _resolve_session_id(
    session_ref: str | None,
    pane_override: str | None = None,
) -> int:
    """Resolve a session reference to its integer id.

    Accepts:
      - None → current session via the unified resolver (E-1294).
        Resolver reads $TMUX_PANE; if `pane_override` is set, $TMUX_PANE
        is temporarily replaced so the resolver sees the caller's
        intended pane (used by the menu binding to redirect away from
        the ephemeral popup's pane).
      - Plain integer string ("356").
      - "ES-NNN" / "es-NNN" / "E-NNN" forms (lenient prefix strip).
    """
    if session_ref is None:
        from endless.task_cmd import _current_endless_session_id
        import os
        prev = os.environ.get("TMUX_PANE")
        if pane_override:
            os.environ["TMUX_PANE"] = pane_override
        try:
            eid = _current_endless_session_id()
        finally:
            if pane_override:
                if prev is None:
                    os.environ.pop("TMUX_PANE", None)
                else:
                    os.environ["TMUX_PANE"] = prev
        if eid is None:
            raise click.ClickException(
                "No current session resolvable. Pass an explicit session id "
                "(e.g. `endless session activity ES-356`)."
            )
        return eid

    ref = session_ref.strip()
    # Lenient prefix strip: ES-, es-, E-, e- (the last covers the
    # existing E-NNN session-id form before the E-1261 ES- migration).
    for prefix in ("ES-", "es-", "E-", "e-"):
        if ref.startswith(prefix):
            ref = ref[len(prefix):]
            break

    if ref.isdigit():
        return int(ref)
    raise click.ClickException(
        f"Unrecognized session reference: {session_ref!r}"
    )


def _project_root_for_cwd() -> Path:
    """Locate the Endless project root for the current cwd.

    Reuses the same resolver as session_cmd so behavior is consistent.
    """
    from endless.session_cmd import _project_root_for_cwd as _impl
    return _impl()


def _read_session_events(project_root: Path, session_id: str) -> list[dict]:
    """Scan all ledger segment files for events emitted by this session."""
    ledger_dir = project_root / ".endless" / "db-ledger"
    if not ledger_dir.is_dir():
        return []

    events: list[dict] = []
    for path in sorted(ledger_dir.glob("db-entries-*.jsonl")):
        with path.open() as fh:
            for raw in fh:
                line = raw.strip()
                if not line:
                    continue
                try:
                    evt = json.loads(line)
                except json.JSONDecodeError:
                    continue
                actor = evt.get("actor") or {}
                if actor.get("session_id") == session_id:
                    events.append(evt)
    return events


def _render(session_id: int, events: list[dict]) -> None:
    """Group events and print the archetype-2 report."""
    header = _header_line(session_id, len(events))
    click.echo(header)
    click.echo()

    filed: list[str] = []
    decisions: list[str] = []
    claimed: list[str] = []
    released: list[str] = []
    by_status: dict[str, list[str]] = {s: [] for s in _REPORTED_STATUSES}
    dep_created = 0
    dep_deleted = 0
    fields_updated = 0

    for evt in events:
        kind = evt.get("kind", "")
        entity_id = (evt.get("entity") or {}).get("id", "")
        payload = evt.get("payload") or {}

        if kind == "task.created":
            if payload.get("type") == "decision":
                decisions.append(entity_id)
            else:
                filed.append(entity_id)
        elif kind == "task.claimed":
            claimed.append(entity_id)
        elif kind == "task.released":
            released.append(entity_id)
        elif kind == "task.status_changed":
            new_status = payload.get("new_status", "")
            if new_status in by_status:
                by_status[new_status].append(entity_id)
        elif kind == "task.fields_updated":
            fields_updated += 1
        elif kind == "task_dep.created":
            dep_created += 1
        elif kind == "task_dep.deleted":
            dep_deleted += 1

    any_output = False
    for label, ids in (
        ("Filed", filed),
        ("Decisions", decisions),
        ("Claimed", claimed),
    ):
        if ids:
            click.echo(_format_group(label, ids))
            any_output = True

    for status in _REPORTED_STATUSES:
        ids = by_status[status]
        if ids:
            click.echo(_format_group(_REPORTED_STATUS_LABELS[status], ids))
            any_output = True

    if released:
        click.echo(_format_group("Released", released))
        any_output = True

    if dep_created or dep_deleted:
        click.echo(f"  Dependencies: +{dep_created} / −{dep_deleted}")
        any_output = True

    if fields_updated:
        click.echo(f"  Field updates: {fields_updated}")
        any_output = True

    if not any_output:
        click.echo("  (no recorded activity)")


def _format_group(label: str, ids: list[str]) -> str:
    """Render one group line, deduping ids while preserving first-seen order.

    A task that transitions through multiple statuses appears once per
    group (e.g. once under Filed, once under Claimed, once under
    Confirmed) — but only once within each.
    """
    unique = list(dict.fromkeys(ids))
    formatted = ", ".join(f"E-{i}" for i in unique)
    return f"  {label} ({len(unique)}): {formatted}"


def _header_line(session_id: int, event_count: int) -> str:
    """Build the report header from the sessions row metadata."""
    rows = db.query(
        "SELECT COALESCE(p.name, '') AS project, "
        "       COALESCE(s.started_at, '') AS started "
        "FROM sessions s LEFT JOIN projects p ON p.id = s.project_id "
        "WHERE s.id = ?",
        (session_id,),
    )
    if not rows:
        return (
            f"Session ES-{session_id} — (no session row found) "
            f"— {event_count} event(s)"
        )
    project = rows[0]["project"] or "(no project)"
    started = rows[0]["started"] or "?"
    return (
        f"Session ES-{session_id} — {project} — "
        f"started {started} — {event_count} event(s)"
    )
