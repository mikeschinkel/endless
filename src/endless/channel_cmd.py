"""Inter-session messaging — beacon, connect, send messages between Claude sessions."""

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


def _resolve_session() -> str:
    """Resolve the current session ID from TMUX_PANE."""
    pane = os.environ.get("TMUX_PANE")
    if not pane:
        raise click.ClickException(
            "Not in a tmux session. Inter-session messaging requires tmux."
        )

    row = db.query(
        "SELECT session_id FROM ai_sessions "
        "WHERE tmux_pane = ? AND state != 'ended' "
        "ORDER BY last_activity DESC LIMIT 1",
        (pane,),
    )
    if not row:
        raise click.ClickException(
            f"No active session found for tmux pane {pane}. "
            "Run 'endless plan start <id>' first."
        )
    return row[0]["session_id"]


def _tmux_nudge(pane: str, text: str):
    """Send a nudge to a tmux pane to trigger UserPromptSubmit."""
    try:
        subprocess.run(
            ["tmux", "send-keys", "-t", pane, text, "Enter"],
            check=True,
            capture_output=True,
        )
    except subprocess.CalledProcessError:
        click.echo(
            click.style("!", fg="yellow")
            + f" Could not nudge pane {pane} (may have closed)"
        )


def beacon(project_name: str | None = None):
    """Announce this session as available for messaging."""
    import uuid

    project_id, proj_name = _resolve_project(project_name)
    session_id = _resolve_session()
    pane = os.environ["TMUX_PANE"]

    channel_id = str(uuid.uuid4())[:8]
    db.execute(
        "INSERT INTO msg_channels "
        "(channel_id, session_a, pane_a, project_id, state) "
        "VALUES (?, ?, ?, ?, 'beacon')",
        (channel_id, session_id, pane, project_id),
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
            f"endless channel connect {channel_id}",
            dim=True,
        )
    )


def connect(channel_id: str | None = None):
    """Connect to a beaconing session."""
    session_id = _resolve_session()
    pane = os.environ["TMUX_PANE"]

    if channel_id:
        row = db.query(
            "SELECT channel_id, session_a, pane_a FROM msg_channels "
            "WHERE channel_id = ? AND state = 'beacon'",
            (channel_id,),
        )
        if not row:
            raise click.ClickException(
                f"Channel {channel_id} not found or not in beacon state."
            )
    else:
        row = db.query(
            "SELECT channel_id, session_a, pane_a FROM msg_channels "
            "WHERE state = 'beacon' ORDER BY created_at DESC",
        )
        if not row:
            raise click.ClickException("No active beacons found.")
        if len(row) > 1:
            raise click.ClickException(
                f"Multiple beacons active ({len(row)}). "
                "Specify a channel_id: endless msg connect <channel_id>"
            )
        channel_id = row[0]["channel_id"]

    target_pane = row[0]["pane_a"]

    db.execute(
        "UPDATE msg_channels SET session_b=?, pane_b=?, "
        "state='connected', connected_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
        "WHERE channel_id=?",
        (session_id, pane, channel_id),
    )

    click.echo(
        click.style("•", fg="green")
        + f" Connected to channel "
        + click.style(channel_id, bold=True)
    )

    # Nudge the beacon session
    _tmux_nudge(target_pane, f"[connected: {channel_id}]")


def send(message: str):
    """Send a message to the connected session."""
    session_id = _resolve_session()

    # Find the active channel for this session
    row = db.query(
        "SELECT channel_id, session_a, pane_a, session_b, pane_b "
        "FROM msg_channels "
        "WHERE state = 'connected' "
        "AND (session_a = ? OR session_b = ?) "
        "ORDER BY connected_at DESC LIMIT 1",
        (session_id, session_id),
    )
    if not row:
        raise click.ClickException(
            "No active channel. Run 'endless channel beacon' or "
            "'endless channel connect' first."
        )

    channel = row[0]
    channel_id = channel["channel_id"]

    # Determine target pane
    if session_id == channel["session_a"]:
        target_pane = channel["pane_b"]
    else:
        target_pane = channel["pane_a"]

    # Queue the message
    db.execute(
        "INSERT INTO msg_queue (channel_id, sender, body, status) "
        "VALUES (?, ?, ?, 'queued')",
        (channel_id, session_id, message),
    )

    click.echo(
        click.style("•", fg="cyan")
        + " Message sent on channel "
        + click.style(channel_id, bold=True)
    )

    # Nudge the target
    _tmux_nudge(target_pane, "[You have a pending inter-session message. Run: endless channel inbox]")


def inbox():
    """Show pending messages for the current session."""
    session_id = _resolve_session()

    rows = db.query(
        "SELECT mq.id, mq.channel_id, mq.sender, mq.body, mq.created_at "
        "FROM msg_queue mq "
        "JOIN msg_channels mc ON mq.channel_id = mc.channel_id "
        "WHERE mq.status = 'queued' "
        "AND mq.sender != ? "
        "AND mc.state = 'connected' "
        "AND (mc.session_a = ? OR mc.session_b = ?) "
        "ORDER BY mq.created_at ASC",
        (session_id, session_id, session_id),
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
            "UPDATE msg_queue SET status='delivered', "
            "delivered_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
            "WHERE id = ?",
            (row['id'],),
        )

    click.echo(
        click.style(f"\n  {len(rows)} message(s) delivered", dim=True)
    )


def close():
    """Close the active channel for the current session."""
    session_id = _resolve_session()

    row = db.query(
        "SELECT channel_id FROM msg_channels "
        "WHERE state = 'connected' "
        "AND (session_a = ? OR session_b = ?) "
        "ORDER BY connected_at DESC LIMIT 1",
        (session_id, session_id),
    )
    if not row:
        raise click.ClickException("No active channel to close.")

    channel_id = row[0]["channel_id"]
    db.execute(
        "UPDATE msg_channels SET state='closed', "
        "closed_at=strftime('%Y-%m-%dT%H:%M:%S','now') "
        "WHERE channel_id=?",
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
            "SELECT mc.channel_id, mc.session_a, mc.pane_a, mc.created_at, "
            "p.name as project_name "
            "FROM msg_channels mc "
            "LEFT JOIN projects p ON mc.project_id = p.id "
            "WHERE mc.state = 'beacon' AND mc.project_id = ? "
            "ORDER BY mc.created_at DESC",
            (project_id,),
        )
    else:
        rows = db.query(
            "SELECT mc.channel_id, mc.session_a, mc.pane_a, mc.created_at, "
            "p.name as project_name "
            "FROM msg_channels mc "
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
            click.style("  " + row["channel_id"], bold=True)
            + f"  {proj}  pane={row['pane_a']}  "
            + click.style(row["created_at"], dim=True)
        )

    click.echo(
        click.style(
            f"\n  {len(rows)} beacon(s). "
            "Connect with: endless msg connect <channel_id>",
            dim=True,
        )
    )
