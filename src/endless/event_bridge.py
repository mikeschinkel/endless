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
