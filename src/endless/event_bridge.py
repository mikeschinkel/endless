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


def emit_event(
    kind: str,
    project: str,
    entity_type: str,
    entity_id: str | int,
    payload: dict,
    actor_kind: str = "cli",
    actor_id: str | None = None,
    project_root: str | None = None,
    correlation_id: str | None = None,
) -> dict | None:
    """Shell out to endless-event to write an event and execute the DB mutation.

    Returns the parsed JSON output from endless-event (contains ts, kind, and
    optionally id for created tasks), or None if stdout was empty.

    Raises click.ClickException on failure.
    """
    node_id = _get_or_create_node_id()

    if actor_id is None:
        actor_id = f"{os.getenv('USER', 'unknown')}@{socket.gethostname()}"

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

    if correlation_id:
        cmd.extend(["--cid", correlation_id])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        error_msg = result.stderr.strip()
        raise click.ClickException(f"Event write failed: {error_msg}")

    if result.stdout.strip():
        return json.loads(result.stdout.strip())
    return None


def _get_or_create_node_id() -> str:
    """Read node_id from config.json, or generate and persist one."""
    config_path = Path.home() / ".config" / "endless" / "config.json"
    if not config_path.exists():
        raise click.ClickException(
            f"Config not found at {config_path}. Run 'endless scan' first."
        )

    data = json.loads(config_path.read_text())
    if "node_id" not in data:
        data["node_id"] = secrets.token_hex(2)  # 4 hex chars
        config_path.write_text(json.dumps(data, indent=2) + "\n")

    return data["node_id"]
