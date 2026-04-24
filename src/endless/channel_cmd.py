"""Inter-session messaging — beacon, connect, send messages between Claude sessions."""

import json
import os
import subprocess
from pathlib import Path

import click

from endless import db, config


def _resolve_project(name: str | None) -> tuple[int, str]:
    """Resolve project name, return (id, name)."""
    if not name:
        cwd = Path.cwd()
        pcfg = config.project_config_read(cwd)
        if pcfg:
            name = pcfg.get("name")
        if not name:
            row = db.query(
                "SELECT name FROM projects WHERE path = ?",
                (str(cwd),),
            )
            if row:
                name = row[0]["name"]
        if not name:
            raise click.ClickException(
                "Not in a registered project directory. "
                "Use --project to specify."
            )

    row = db.query(
        "SELECT id, name FROM projects WHERE name = ?",
        (name,),
    )
    if not row:
        raise click.ClickException(f"No project found with name '{name}'")
    return row[0]["id"], row[0]["name"]


def _resolve_process() -> str:
    """Resolve the current process identifier. Uses TMUX_PANE when in tmux."""
    pane = os.environ.get("TMUX_PANE")
    if pane:
        return pane
    raise click.ClickException(
        "Not in a tmux session. Inter-session messaging requires tmux "
        "(non-tmux support planned)."
    )


def _channel_notify(target_process: str, event: str, channel_id: str, preview: str):
    """Push a notification to the target session's MCP channel plugin via HTTP.
    Falls back to tmux send-keys if no channel plugin is registered.
    target_process is the process identifier (TMUX_PANE or similar)."""
    import urllib.request

    row = db.query(
        "SELECT port, pid FROM channels WHERE process = ?",
        (target_process,),
    )
    if row:
        port = row[0]["port"]
        pid = row[0]["pid"]
        # Check if the process is still alive
        alive = _pid_alive(pid)
        if alive:
            try:
                data = json.dumps({
                    "event": event,
                    "channel_id": channel_id,
                    "preview": preview,
                }).encode()
                req = urllib.request.Request(
                    f"http://127.0.0.1:{port}/notify",
                    data=data,
                    headers={"Content-Type": "application/json"},
                )
                urllib.request.urlopen(req, timeout=2)
                return
            except Exception:
                click.echo(
                    click.style("!", fg="yellow")
                    + f" Channel plugin on port {port} not reachable, falling back to tmux"
                )
        else:
            # Stale entry — clean it up
            db.execute(
                "DELETE FROM channels WHERE process = ?",
                (target_process,),
            )

    # Fallback: tmux send-keys (only works if target_process looks like a pane)
    if target_process.startswith("%"):
        try:
            subprocess.run(
                ["tmux", "send-keys", "-t", target_process, preview, "Enter"],
                check=True,
                capture_output=True,
            )
        except subprocess.CalledProcessError:
            click.echo(
                click.style("!", fg="yellow")
                + f" Could not nudge pane {target_process} (may have closed)"
            )
    else:
        click.echo(
            click.style("!", fg="yellow")
            + " No channel plugin and no tmux pane for target session"
        )


def _pid_alive(pid: int) -> bool:
    """Check if a process is still running."""
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def beacon(project_name: str | None = None):
    """Announce this session as available for messaging."""
    import uuid

    project_id, proj_name = _resolve_project(project_name)
    process = _resolve_process()

    channel_id = str(uuid.uuid4())[:8]
    db.execute(
        "INSERT INTO conversations "
        "(conversation_id, process_a, project_id, state) "
        "VALUES (?, ?, ?, 'beacon')",
        (channel_id, process, project_id),
    )

    click.echo(
        click.style("•", fg="cyan")
        + " Beacon active for "
        + click.style(proj_name, bold=True)
    )
    click.echo(
        click.style("  Channel: ", dim=True)
        + click.style(channel_id, bold=True)
    )
    click.echo(
        click.style(
            "  Other session connects with: "
            "endless channel connect",
            dim=True,
        )
    )


def connect(channel_id: str | None = None):
    """Connect to a beaconing session."""
    process = _resolve_process()

    if channel_id:
        # Explicit channel_id — check existence first, then state
        row = db.query(
            "SELECT conversation_id, process_a, state FROM conversations "
            "WHERE conversation_id = ?",
            (channel_id,),
        )
        if not row:
            raise click.ClickException(
                f"Channel {channel_id} not found."
            )
        if row[0]["state"] != "beacon":
            raise click.ClickException(
                f"Channel {channel_id} exists but is not in beacon state "
                f"(current state: {row[0]['state']})."
            )
    else:
        # Auto-detect: find beacons for the current project
        project_id, proj_name = _resolve_project(None)
        row = db.query(
            "SELECT conversation_id, process_a FROM conversations "
            "WHERE state = 'beacon' AND project_id = ? "
            "ORDER BY created_at DESC",
            (project_id,),
        )
        if not row:
            raise click.ClickException(
                f"No active beacons found for project '{proj_name}'. "
                "Ask the other session to run: endless channel beacon"
            )
        if len(row) > 1:
            click.echo(
                click.style("Multiple beacons for ", fg="yellow")
                + click.style(proj_name, bold=True)
                + click.style(":", fg="yellow")
            )
            for r in row:
                click.echo(f"  {r['conversation_id']}")
            raise click.ClickException(
                "Specify a channel_id: endless channel connect <channel_id>"
            )
        channel_id = row[0]["conversation_id"]

    target_process = row[0]["process_a"]

    db.execute(
        "UPDATE conversations SET process_b=?, "
        "state='connected', connected_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
        "WHERE conversation_id=?",
        (process, channel_id),
    )

    click.echo(
        click.style("•", fg="green")
        + f" Connected to channel "
        + click.style(channel_id, bold=True)
    )

    # Notify the beacon session
    _channel_notify(
        target_process, "connected", channel_id,
        f"[connected: {channel_id}]",
    )


def send(message: str):
    """Send a message to the connected session."""
    process = _resolve_process()

    # Find the active conversation for this process
    row = db.query(
        "SELECT conversation_id, process_a, process_b "
        "FROM conversations "
        "WHERE state = 'connected' "
        "AND (process_a = ? OR process_b = ?) "
        "ORDER BY connected_at DESC LIMIT 1",
        (process, process),
    )
    if not row:
        raise click.ClickException(
            "No active channel. Run 'endless channel beacon' or "
            "'endless channel connect' first."
        )

    channel = row[0]
    channel_id = channel["conversation_id"]

    # Determine target process identifier
    if process == channel["process_a"]:
        target_process = channel["process_b"]
    else:
        target_process = channel["process_a"]

    # Queue the message
    db.execute(
        "INSERT INTO messages (conversation_id, sender, body, status) "
        "VALUES (?, ?, ?, 'queued')",
        (channel_id, process, message),
    )

    click.echo(
        click.style("•", fg="cyan")
        + " Message sent on channel "
        + click.style(channel_id, bold=True)
    )

    # Notify the target
    _channel_notify(
        target_process, "message", channel_id,
        "You have a pending inter-session message. Run: endless channel inbox",
    )


def inbox():
    """Show pending messages for the current session."""
    process = _resolve_process()

    rows = db.query(
        "SELECT mq.id, mq.conversation_id, mq.sender, mq.body, mq.created_at "
        "FROM messages mq "
        "JOIN conversations mc ON mq.conversation_id = mc.conversation_id "
        "WHERE mq.status = 'queued' "
        "AND mq.sender != ? "
        "AND mc.state = 'connected' "
        "AND (mc.process_a = ? OR mc.process_b = ?) "
        "ORDER BY mq.created_at ASC",
        (process, process, process),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan") + " No pending messages"
        )
        return

    for row in rows:
        click.echo(
            click.style(f"  [{row['created_at']}]", dim=True)
            + f" {row['body']}"
        )

    # Mark as delivered
    for row in rows:
        db.execute(
            "UPDATE messages SET status='delivered', "
            "delivered_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
            "WHERE id = ?",
            (row['id'],),
        )

    click.echo(
        click.style(f"\n  {len(rows)} message(s) delivered", dim=True)
    )


def close():
    """Close the active channel for the current session."""
    process = _resolve_process()

    row = db.query(
        "SELECT conversation_id, state FROM conversations "
        "WHERE state IN ('connected', 'beacon') "
        "AND (process_a = ? OR process_b = ?) "
        "ORDER BY created_at DESC LIMIT 1",
        (process, process),
    )
    if not row:
        raise click.ClickException("No active channel to close.")

    channel_id = row[0]["conversation_id"]
    db.execute(
        "UPDATE conversations SET state='closed', "
        "closed_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
        "WHERE conversation_id=?",
        (channel_id,),
    )

    click.echo(
        click.style("•", fg="cyan")
        + f" Closed channel "
        + click.style(channel_id, bold=True)
    )


def list_beacons(project_name: str | None = None):
    """List active beacons."""
    if project_name:
        project_id, _ = _resolve_project(project_name)
        rows = db.query(
            "SELECT mc.conversation_id, mc.process_a, mc.created_at, "
            "p.name as project_name "
            "FROM conversations mc "
            "LEFT JOIN projects p ON mc.project_id = p.id "
            "WHERE mc.state = 'beacon' AND mc.project_id = ? "
            "ORDER BY mc.created_at DESC",
            (project_id,),
        )
    else:
        rows = db.query(
            "SELECT mc.conversation_id, mc.process_a, mc.created_at, "
            "p.name as project_name "
            "FROM conversations mc "
            "LEFT JOIN projects p ON mc.project_id = p.id "
            "WHERE mc.state = 'beacon' "
            "ORDER BY mc.created_at DESC",
        )

    if not rows:
        click.echo(
            click.style("•", fg="cyan") + " No active beacons"
        )
        return

    for row in rows:
        proj = row["project_name"] or "unknown"
        click.echo(
            click.style("  " + row["conversation_id"], bold=True)
            + f"  {proj}  process={row['process_a']}  "
            + click.style(row["created_at"], dim=True)
        )

    click.echo(
        click.style(
            f"\n  {len(rows)} beacon(s). "
            "Connect with: endless msg connect <channel_id>",
            dim=True,
        )
    )
