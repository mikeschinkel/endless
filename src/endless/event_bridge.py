"""Bridge to the endless-event Go binary for event-sourced writes."""

import json
import os
import secrets
import shutil
import socket
import subprocess
from pathlib import Path

import click

from endless import config


# Actor kinds for which session attribution is mandatory. If the resolver
# cannot produce a session_id for these kinds, emit_event refuses to fire
# rather than silently losing attribution downstream (E-1401).
#   - "cli":    interactive commands from a Claude pane; the resolver MUST
#               find a binding or the event is unattributable.
#   - "hook":   Claude hook callbacks; same as cli — bound to a session.
#   - "system": cron / migrations / one-shot tools; no session expected.
#   - "web":    web UI; attribution is via user_id, not session.
_ATTRIBUTION_REQUIRED: frozenset[str] = frozenset({"cli", "hook"})


def emit_event(
    kind: str,
    project: str,
    entity_type: str,
    entity_id: str | int,
    payload: dict,
    actor_kind: str = "cli",
    actor_id: str | None = None,
    session_id: str | None = None,
    project_root: str | None = None,
    correlation_id: str | None = None,
) -> dict | None:
    """Shell out to endless-event to write an event and execute the DB mutation.

    Returns the parsed JSON output from endless-event (contains ts, kind, and
    optionally id for created tasks), or None if stdout was empty.

    `session_id` populates `actor.session_id` on the emitted event so per-
    session activity queries can later filter events by session. When None,
    the resolver `_current_endless_session_id()` (from task_cmd) is called
    automatically — so most callers don't need to pass it.

    For actor_kind in {"cli", "hook"}, an unresolvable session_id is a hard
    error: emit_event raises click.ClickException with an actionable message
    rather than firing an event whose attribution will be silently dropped
    (E-1401). For actor_kind in {"system", "web"}, an empty session_id is
    fine — system has no session, web attributes via user_id.

    Raises click.ClickException on failure.
    """
    node_id = _get_or_create_node_id()

    if actor_id is None:
        actor_id = f"{os.getenv('USER', 'unknown')}@{socket.gethostname()}"

    # Track whether session_id was provided by the caller. An explicit None
    # is still "resolver-derived" — the gate only fires when both the caller
    # AND the resolver couldn't produce one.
    if session_id is None:
        # Defer to the unified resolver. As of E-1294 it does the full
        # 3-layer lookup (env / pane-direct / single-sibling), so no
        # inline fallback is needed here.
        try:
            from endless.task_cmd import _current_endless_session_id
            eid = _current_endless_session_id()
            if eid is not None:
                session_id = str(eid)
        except Exception:
            # task_cmd not importable here yet (during early bootstrap),
            # or resolver errored — neither should block event emission
            # here; the gate below handles refusal for kinds that require
            # attribution.
            session_id = None

    if actor_kind in _ATTRIBUTION_REQUIRED and not session_id:
        raise click.ClickException(
            "Cannot determine the Endless session for this pane.\n\n"
            "To fix, do one of:\n"
            "  - Run this command from a Claude session pane.\n"
            "  - Export ENDLESS_SESSION_ID=<id> in this shell; "
            "see `endless session show` for live session ids.\n"
        )

    if project_root is None:
        # Look up the project's registered path so events always land in the
        # main repo, even when invoked from inside a git worktree (where cwd
        # is the worktree, not the project root). Fall back to cwd only if
        # the project isn't registered (defensive; shouldn't happen in normal
        # flow since callers always pass a known project name).
        from endless import db
        row = db.query(
            "SELECT path FROM projects WHERE name = ? LIMIT 1",
            (project,),
        )
        if row:
            project_root = str(Path(row[0]["path"]).expanduser())
        else:
            project_root = str(Path.cwd())

    event_bin = shutil.which("endless-event")
    if not event_bin:
        raise click.ClickException(
            "endless-event binary not found on PATH. "
            "Build it: just build"
        )

    cmd = [
        event_bin, "emit",
        "--kind", kind,
        "--project", project,
        "--entity-type", entity_type,
        "--entity-id", str(entity_id),
        "--actor-kind", actor_kind,
        "--actor-id", actor_id,
        "--node-id", node_id,
        "--project-root", project_root,
        "--payload", json.dumps(payload),
    ]

    if session_id:
        cmd.extend(["--session-id", session_id])

    if correlation_id:
        cmd.extend(["--cid", correlation_id])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        error_msg = result.stderr.strip()
        raise click.ClickException(f"Event write failed: {error_msg}")

    if result.stdout.strip():
        return json.loads(result.stdout.strip())
    return None


def migrate_db(
    dry_run: bool = False,
    force_rebuild: bool = False,
    target: int = 0,
) -> dict:
    """Shell out to `endless-event migrate-db` and return parsed JSON.

    Raises click.ClickException on failure (binary missing or non-zero exit).
    """
    event_bin = shutil.which("endless-event")
    if not event_bin:
        raise click.ClickException(
            "endless-event binary not found on PATH. Build it: just build"
        )

    cmd = [event_bin, "migrate-db"]
    if dry_run:
        cmd.append("--dry-run")
    if force_rebuild:
        cmd.append("--force-rebuild")
    if target > 0:
        cmd.extend(["--target", str(target)])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        # endless-event prints JSON with an "error" field on failure.
        try:
            payload = json.loads(result.stdout.strip()) if result.stdout.strip() else {}
        except json.JSONDecodeError:
            payload = {}
        msg = payload.get("error") or result.stderr.strip() or "migrate-db failed"
        raise click.ClickException(f"migrate-db failed: {msg}")

    if not result.stdout.strip():
        return {"applied": [], "skipped": []}
    return json.loads(result.stdout.strip())


def _get_or_create_node_id() -> str:
    """Read node_id from config.json, or generate and persist one."""
    config_path = config.CONFIG_FILE
    if not config_path.exists():
        raise click.ClickException(
            f"Config not found at {config_path}. Run 'endless scan' first."
        )

    data = json.loads(config_path.read_text())
    if "node_id" not in data:
        data["node_id"] = secrets.token_hex(2)  # 4 hex chars
        config_path.write_text(json.dumps(data, indent=2) + "\n")

    return data["node_id"]
