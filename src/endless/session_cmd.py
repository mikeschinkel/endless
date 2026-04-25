"""Session command logic — history, list, search, reimport."""

import json as json_mod
import os
from pathlib import Path

import click

from endless import db


def _format_tool_content(content: str, tool_name: str | None = None, mode: str = "truncated") -> str:
    """Format tool_use content for display.

    mode: "truncated" (name + description), "full" (everything), "oneline" (search results)
    """
    name = tool_name or "unknown"

    # Try to parse the JSON content to extract description and command
    try:
        # Content format is "ToolName: {json}" — extract the JSON part
        json_part = content
        if ": " in content:
            _, json_part = content.split(": ", 1)
        parsed = json_mod.loads(json_part)
    except (json_mod.JSONDecodeError, ValueError):
        parsed = None

    if not parsed:
        if mode == "oneline":
            return f"{name}: {content[:80]}"
        return f"{name}: {content[:200]}"

    desc = parsed.get("description", "")
    cmd = parsed.get("command", "")
    file_path = parsed.get("file_path", "")
    pattern = parsed.get("pattern", "")

    if mode == "oneline":
        detail = desc or cmd or file_path or pattern or ""
        if detail:
            return f"{name} — {detail[:60]}"
        return name

    # truncated or full
    parts = [name]
    if desc:
        parts.append(desc)
    if cmd:
        parts.append(cmd)
    elif file_path:
        parts.append(file_path)
    elif pattern:
        parts.append(pattern)

    if mode == "truncated":
        return "\n".join(parts[:3])
    return "\n".join(parts)


def _resolve_session(value: str) -> dict:
    """Resolve a session by integer ID, short UUID prefix, or full UUID."""
    # Try integer ID first
    try:
        int_id = int(value)
        row = db.query(
            "SELECT id, session_id, project_id, state, summary, "
            "transcript_path, started_at, last_activity, hidden "
            "FROM sessions WHERE id = ?",
            (int_id,),
        )
        if row:
            return dict(row[0])
    except ValueError:
        pass

    # Try exact UUID match
    row = db.query(
        "SELECT id, session_id, project_id, state, summary, "
        "transcript_path, started_at, last_activity, hidden "
        "FROM sessions WHERE session_id = ?",
        (value,),
    )
    if row:
        return dict(row[0])

    # Try UUID prefix match
    row = db.query(
        "SELECT id, session_id, project_id, state, summary, "
        "transcript_path, started_at, last_activity, hidden "
        "FROM sessions WHERE session_id LIKE ?",
        (value + "%",),
    )
    if not row:
        raise click.ClickException(f"No session found matching '{value}'")
    if len(row) > 1:
        matches = ", ".join(r["session_id"][:12] for r in row[:5])
        raise click.ClickException(
            f"Ambiguous session prefix '{value}' — matches: {matches}. "
            "Use more characters."
        )
    return dict(row[0])


def show_history(
    session_value: str,
    show_tools: str | None = None,
    show_timestamps: bool = False,
    limit: int = 20,
    sort_asc: bool = False,
    as_json: bool = False,
):
    """Show conversation history for a session."""
    session = _resolve_session(session_value)
    session_id = session["session_id"]

    # Build query
    where = "WHERE session_id = ?"
    params: list = [session_id]

    if not show_tools:
        where += " AND role != 'tool_use'"

    order = "ASC" if sort_asc else "DESC"
    rows = db.query(
        f"SELECT id, role, content, tool_name, created_at "
        f"FROM session_messages {where} "
        f"ORDER BY created_at {order}, id {order} "
        f"LIMIT ?",
        tuple(params + [limit]),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan")
            + f" No messages for session {session['id']}"
        )
        return

    if as_json:
        import json
        out = [
            {
                "id": r["id"],
                "role": r["role"],
                "content": r["content"],
                "tool_name": r["tool_name"],
                "created_at": r["created_at"],
            }
            for r in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    # If reverse chron, reverse for display so newest is at bottom (natural reading)
    if not sort_asc:
        rows = list(reversed(rows))

    for row in rows:
        role = row["role"]
        content = row["content"]

        if role == "tool_use":
            mode = "full" if show_tools == "full" else "truncated"
            formatted = _format_tool_content(content, row["tool_name"], mode)
            lines = formatted.split("\n")
            if show_timestamps:
                ts = _format_ts(row["created_at"])
                click.echo(click.style(f"  Tool: ", fg="yellow", dim=True) + click.style(lines[0], dim=True))
                for line in lines[1:]:
                    click.echo(click.style(f"  {line}", dim=True))
            else:
                click.echo(click.style(f"  Tool: ", fg="yellow", dim=True) + click.style(lines[0], dim=True))
                for line in lines[1:]:
                    click.echo(click.style(f"  {line}", dim=True))
            click.echo()
            continue

        if role == "user":
            label = "User"
            label_color = "cyan"
            label_upper = "USER"
        else:
            label = "Claude"
            label_color = "green"
            label_upper = "CLAUDE"

        if show_timestamps:
            ts = _format_ts(row["created_at"])
            click.echo(click.style(f"{label_upper} [{ts}]", fg=label_color))
            click.echo(content)
        else:
            click.echo(
                click.style(f"{label}: ", fg=label_color, dim=True)
                + content.split("\n")[0]
            )
            # Print remaining lines without label
            for line in content.split("\n")[1:]:
                click.echo(line)

        click.echo()


def list_sessions(
    project_name: str | None = None,
    show_all: bool = False,
    show_hidden: bool = False,
    show_empty: bool = False,
    state_filter: str | None = None,
    sort_by: str | None = None,
    limit: int = 20,
    as_json: bool = False,
):
    """List recent sessions."""
    where = "WHERE 1=1"
    params: list = []

    if show_hidden:
        where += " AND s.hidden = 1"
    elif not show_all:
        where += " AND s.hidden = 0"

    if project_name:
        where += " AND p.name = ?"
        params.append(project_name)

    if state_filter:
        where += " AND s.state = ?"
        params.append(state_filter)

    if not show_all:
        # Only show sessions from registered projects (unless --all)
        if not project_name:
            where += " AND s.project_id IS NOT NULL"

        if not show_empty:
            # Filter out empty sessions
            where += (
                " AND (SELECT count(*) FROM session_messages m "
                "WHERE m.session_id = s.session_id) > 0"
            )
            # Exclude sessions created by 'endless session recap' (claude -p calls)
            where += (
                " AND NOT EXISTS (SELECT 1 FROM session_messages m "
                "WHERE m.session_id = s.session_id AND m.role = 'user' "
                "AND m.content LIKE 'Summarize this conversation in 2-3 sentences%' "
                "ORDER BY m.created_at ASC LIMIT 1)"
            )

    sort_map = {
        "id": "s.id DESC",
        "project": "project_name, s.id DESC",
        "state": ("CASE s.state WHEN 'working' THEN 0 WHEN 'needs_input' THEN 1 "
                  "WHEN 'idle' THEN 2 WHEN 'ended' THEN 3 END, "
                  "COALESCE(s.last_activity, s.started_at) DESC"),
        "count": "msg_count DESC",
    }
    # Default sort: state priority (working first, ended last), then recency
    order = sort_map.get(sort_by, sort_map["state"])

    params.append(limit)

    rows = db.query(
        f"SELECT s.id, s.session_id, s.state, s.summary, "
        f"s.started_at, s.last_activity, s.hidden, "
        f"COALESCE(p.name, '') as project_name, "
        f"(SELECT count(*) FROM session_messages m WHERE m.session_id = s.session_id) as msg_count "
        f"FROM sessions s "
        f"LEFT JOIN projects p ON s.project_id = p.id "
        f"{where} "
        f"ORDER BY {order} "
        f"LIMIT ?",
        tuple(params),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan") + " No sessions found"
        )
        return

    if as_json:
        import json
        out = [
            {
                "id": r["id"],
                "session_id": r["session_id"][:12],
                "project": r["project_name"],
                "state": r["state"],
                "messages": r["msg_count"],
                "summary": r["summary"] or "",
                "started": r["started_at"],
            }
            for r in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    try:
        term_width = os.get_terminal_size().columns
    except OSError:
        term_width = 120

    # Get total count for header
    total_count = db.scalar(
        f"SELECT count(*) FROM sessions s "
        f"LEFT JOIN projects p ON s.project_id = p.id "
        f"{where}",
        tuple(params[:-1]),  # exclude limit param
    ) or 0

    click.echo()
    if total_count > len(rows):
        click.echo(click.style(f"Sessions ({len(rows)} of {total_count})", bold=True))
    else:
        click.echo(click.style("Sessions", bold=True))

    id_w = 4
    proj_w = max(7, max((len(r["project_name"]) for r in rows), default=7))
    state_w = 7
    msg_w = 4
    gap = "  "
    fixed = id_w + proj_w + state_w + msg_w + len(gap) * 4
    summary_w = max(20, term_width - fixed)

    header = (
        f"{'ID':<{id_w}}{gap}"
        f"{'Project':<{proj_w}}{gap}"
        f"{'State':<{state_w}}{gap}"
        f"{'Msgs':>{msg_w}}{gap}"
        f"Summary"
    )
    sep = (
        f"{'─' * id_w}{gap}"
        f"{'─' * proj_w}{gap}"
        f"{'─' * state_w}{gap}"
        f"{'─' * msg_w}{gap}"
        f"{'─' * summary_w}"
    )
    click.echo(header)
    click.echo(sep)

    for row in rows:
        if row["msg_count"] == 0:
            summary = "(empty)"
        else:
            summary = row["summary"] or "(no summary)"
        if len(summary) > summary_w:
            summary = summary[:summary_w - 1] + "…"
        line = (
            f"{row['id']:<{id_w}}{gap}"
            f"{row['project_name']:<{proj_w}}{gap}"
            f"{row['state']:<{state_w}}{gap}"
            f"{row['msg_count']:>{msg_w}}{gap}"
            f"{summary}"
        )
        click.echo(line)

    # Notify about sessions needing recaps
    if not as_json:
        recap_count = db.scalar(
            "SELECT count(*) FROM sessions WHERE needs_recap = 1 AND hidden = 0"
        ) or 0
        if recap_count > 0:
            click.echo()
            click.echo(
                click.style(f"  {recap_count} session(s) need recaps. ", dim=True)
                + click.style("Run: endless session recap", fg="cyan", dim=True)
            )

    click.echo()


def search_sessions(
    query: str,
    project_name: str | None = None,
    limit: int = 20,
    as_json: bool = False,
):
    """Search across all session messages using FTS5."""
    where = ""
    params: list = []

    if project_name:
        where = (
            "AND sm.session_id IN ("
            "  SELECT s.session_id FROM sessions s "
            "  JOIN projects p ON s.project_id = p.id "
            "  WHERE p.name = ?"
            ")"
        )
        params.append(project_name)

    params.append(limit)

    rows = db.query(
        f"SELECT sm.id, sm.session_id, sm.role, sm.content, sm.created_at, "
        f"s.id as db_id, COALESCE(p.name, '') as project_name "
        f"FROM session_messages_fts fts "
        f"JOIN session_messages sm ON sm.id = fts.rowid "
        f"JOIN sessions s ON s.session_id = sm.session_id "
        f"LEFT JOIN projects p ON s.project_id = p.id "
        f"WHERE session_messages_fts MATCH ? {where} "
        f"ORDER BY sm.created_at DESC "
        f"LIMIT ?",
        tuple([query] + params),
    )

    if not rows:
        click.echo(
            click.style("•", fg="cyan")
            + f" No messages matching '{query}'"
        )
        return

    if as_json:
        import json
        out = [
            {
                "session_id": r["db_id"],
                "project": r["project_name"],
                "role": r["role"],
                "content": r["content"][:200],
                "created_at": r["created_at"],
            }
            for r in rows
        ]
        click.echo(json.dumps(out, indent=2))
        return

    click.echo()
    click.echo(click.style(f"Search: '{query}'", bold=True))
    click.echo()

    for row in rows:
        role = row["role"]
        content = row["content"]

        # Session context
        meta = click.style(
            f"  [session {row['db_id']}, {row['project_name']}]",
            dim=True,
        )

        if role == "tool_use":
            formatted = _format_tool_content(content, None, "oneline")
            click.echo(
                click.style("Tool: ", fg="yellow", dim=True)
                + click.style(formatted, dim=True)
                + meta
            )
        else:
            role_color = "cyan" if role == "user" else "green"
            role_label = "User" if role == "user" else "Claude"
            # Single line preview
            preview = content.replace("\n", " ")
            if len(preview) > 200:
                preview = preview[:200] + "…"
            click.echo(
                click.style(f"{role_label}: ", fg=role_color, dim=True)
                + preview
                + meta
            )

    click.echo()
    click.echo(click.style(f"{len(rows)} match(es)", dim=True))


def reimport_sessions(session_value: str | None = None):
    """Reimport transcript data from JSONL files."""
    if session_value:
        # Reimport a specific session
        session = _resolve_session(session_value)
        path = session.get("transcript_path") or ""
        if not path:
            # Try to find JSONL by session UUID
            path = _find_jsonl(session["session_id"])
        if not path:
            raise click.ClickException(
                f"No transcript path for session {session['id']}. "
                "Provide the JSONL file path directly."
            )
        # Reset offset and re-parse
        db.execute(
            "UPDATE sessions SET transcript_offset = 0 WHERE session_id = ?",
            (session["session_id"],),
        )
        _parse_transcript_py(session["session_id"], path)
        count = db.scalar(
            "SELECT count(*) FROM session_messages WHERE session_id = ?",
            (session["session_id"],),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Imported session {session['id']}: {count} messages"
        )
        return

    # Reimport all — scan for JSONL files
    claude_dir = Path.home() / ".claude" / "projects"
    if not claude_dir.exists():
        raise click.ClickException(f"No Claude projects dir: {claude_dir}")

    jsonl_files = list(claude_dir.rglob("*.jsonl"))
    if not jsonl_files:
        click.echo(
            click.style("•", fg="cyan") + " No JSONL files found"
        )
        return

    total_messages = 0
    total_sessions = 0

    for jf in jsonl_files:
        # Extract session ID from filename (UUID.jsonl)
        session_id = jf.stem
        if len(session_id) < 36:
            continue  # not a UUID filename

        # Derive project_id from JSONL path
        project_id = _project_id_from_path(str(jf))

        # Ensure session exists in DB
        row = db.query(
            "SELECT id FROM sessions WHERE session_id = ?",
            (session_id,),
        )
        if not row:
            # Create a minimal session record
            if project_id:
                db.execute(
                    "INSERT OR IGNORE INTO sessions (session_id, project_id, state, started_at) "
                    "VALUES (?, ?, 'ended', datetime('now'))",
                    (session_id, project_id),
                )
            else:
                db.execute(
                    "INSERT OR IGNORE INTO sessions (session_id, state, started_at) "
                    "VALUES (?, 'ended', datetime('now'))",
                    (session_id,),
                )
        elif project_id:
            # Backfill project_id if missing
            db.execute(
                "UPDATE sessions SET project_id = ? WHERE session_id = ? AND project_id IS NULL",
                (project_id, session_id),
            )

        # Store transcript path and reset offset for re-parse
        db.execute(
            "UPDATE sessions SET transcript_path = ?, transcript_offset = 0 "
            "WHERE session_id = ?",
            (str(jf), session_id),
        )

        # Parse
        before = db.scalar(
            "SELECT count(*) FROM session_messages WHERE session_id = ?",
            (session_id,),
        ) or 0
        _parse_transcript_py(session_id, str(jf))
        after = db.scalar(
            "SELECT count(*) FROM session_messages WHERE session_id = ?",
            (session_id,),
        ) or 0
        new = after - before
        if new > 0:
            total_messages += new
            total_sessions += 1

    # Backfill summaries for sessions that don't have one
    sessions_needing_summary = db.query(
        "SELECT session_id FROM sessions "
        "WHERE (summary IS NULL OR summary = '') "
        "AND session_id IN (SELECT DISTINCT session_id FROM session_messages WHERE role = 'assistant')"
    )
    for s in sessions_needing_summary:
        sid = s["session_id"]
        first_msg = db.query(
            "SELECT substr(content, 1, 200) as summary FROM session_messages "
            "WHERE session_id = ? AND role = 'assistant' "
            "ORDER BY created_at ASC LIMIT 1",
            (sid,),
        )
        if first_msg and first_msg[0]["summary"]:
            db.execute(
                "UPDATE sessions SET summary = ? WHERE session_id = ?",
                (first_msg[0]["summary"], sid),
            )

    click.echo(
        click.style("•", fg="cyan")
        + f" Added {total_messages} messages across {total_sessions} sessions "
        + f"({len(jsonl_files)} JSONL files scanned)"
    )


def recap_session(session_value: str | None = None, force: bool = False):
    """Generate recap summaries for sessions using claude -p."""
    import subprocess
    import shutil

    claude_bin = shutil.which("claude")
    if not claude_bin:
        raise click.ClickException(
            "claude CLI not found on PATH. Required for recap generation."
        )

    if session_value:
        # Recap a specific session
        session = _resolve_session(session_value)
        _generate_recap(session, claude_bin, force=force)
        return

    # Recap all sessions that need it
    rows = db.query(
        "SELECT id, session_id, summary_seq FROM sessions "
        "WHERE needs_recap = 1 AND hidden = 0"
    )
    if not rows:
        click.echo(
            click.style("•", fg="cyan") + " No sessions need recaps"
        )
        return

    for row in rows:
        session = _resolve_session(str(row["id"]))
        _generate_recap(session, claude_bin, force=False)


def _generate_recap(session: dict, claude_bin: str, force: bool = False):
    """Generate a recap for a single session."""
    import subprocess

    session_id = session["session_id"]
    summary_seq = session.get("summary_seq", 0) or 0

    # Count user messages
    user_count = db.scalar(
        "SELECT count(*) FROM session_messages "
        "WHERE session_id = ? AND role = 'user'",
        (session_id,),
    ) or 0

    # Skip if not enough new messages (unless forced)
    if not force and user_count - summary_seq < 10:
        click.echo(
            click.style("•", fg="cyan")
            + f" Session {session['id']}: only {user_count - summary_seq} new user messages, skipping (need 10)"
        )
        return

    # Get last 20 user+assistant messages
    rows = db.query(
        "SELECT role, content FROM session_messages "
        "WHERE session_id = ? AND role IN ('user', 'assistant') "
        "ORDER BY created_at DESC LIMIT 20",
        (session_id,),
    )
    if not rows:
        click.echo(
            click.style("•", fg="cyan")
            + f" Session {session['id']}: no messages to recap"
        )
        return

    # Reverse to chronological order for the prompt
    rows = list(reversed(rows))

    # Build conversation text for claude -p
    conversation = []
    for row in rows:
        role = "User" if row["role"] == "user" else "Claude"
        content = row["content"]
        if len(content) > 1000:
            content = content[:1000] + "..."
        conversation.append(f"{role}: {content}")

    transcript_text = "\n\n".join(conversation)

    prompt = (
        "Summarize this conversation in 2-3 sentences. "
        "Focus on what was discussed, decided, and accomplished. "
        "Be specific — mention task IDs, feature names, and key decisions. "
        "Start directly with the substance — no filler words like "
        "'Conversation focused on', 'This session covered', 'The discussion', "
        "'In this conversation', 'Recap:', or any other preamble. "
        "Just state what happened.\n\n"
        f"{transcript_text}"
    )

    click.echo(
        click.style("•", fg="cyan")
        + f" Generating recap for session {session['id']}..."
    )

    try:
        # Use --allowedTools "" to prevent tool use, reducing noise.
        # Note: this still creates a session via hooks — we hide those after.
        result = subprocess.run(
            [claude_bin, "-p", prompt, "--allowedTools", ""],
            capture_output=True,
            text=True,
            timeout=60,
        )
        if result.returncode != 0:
            click.echo(
                click.style("  Error: ", fg="red")
                + (result.stderr or "claude -p failed").strip()
            )
            return

        summary = result.stdout.strip()
        if not summary:
            click.echo(
                click.style("  Warning: ", fg="yellow")
                + "empty recap returned"
            )
            return

        # Store recap and update watermark
        db.execute(
            "UPDATE sessions SET summary = ?, summary_seq = ?, needs_recap = 0 "
            "WHERE session_id = ?",
            (summary, user_count, session_id),
        )

        click.echo(
            click.style("  ✓ ", fg="green")
            + summary[:100]
            + ("…" if len(summary) > 100 else "")
        )

    except subprocess.TimeoutExpired:
        click.echo(
            click.style("  Error: ", fg="red")
            + "claude -p timed out after 30s"
        )
    except Exception as e:
        click.echo(
            click.style("  Error: ", fg="red")
            + str(e)
        )


def hide_sessions(session_values: list[str]):
    """Hide sessions from the list."""
    for value in session_values:
        session = _resolve_session(value)
        db.execute(
            "UPDATE sessions SET hidden = 1 WHERE session_id = ?",
            (session["session_id"],),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Hidden session {session['id']}"
        )


def unhide_sessions(session_values: list[str]):
    """Unhide sessions."""
    for value in session_values:
        session = _resolve_session(value)
        db.execute(
            "UPDATE sessions SET hidden = 0 WHERE session_id = ?",
            (session["session_id"],),
        )
        click.echo(
            click.style("•", fg="cyan")
            + f" Unhidden session {session['id']}"
        )


def _project_id_from_path(jsonl_path: str) -> int | None:
    """Derive project_id from a JSONL transcript path.

    Path format: ~/.claude/projects/-Users-mike-Projects-foo/UUID.jsonl
    The encoded CWD uses dashes for path separators.
    """
    import re
    match = re.search(r'/\.claude/projects/([^/]+)/', jsonl_path)
    if not match:
        return None
    encoded = match.group(1)
    decoded = encoded.replace('-', '/')

    # Try exact match against registered projects
    row = db.query("SELECT id, path FROM projects")
    if not row:
        return None
    for p in row:
        if p["path"] == decoded or decoded.startswith(p["path"] + "/") or decoded.startswith(p["path"]):
            return p["id"]
    return None


def _find_jsonl(session_id: str) -> str | None:
    """Find a JSONL file for a session ID by scanning Claude project dirs."""
    claude_dir = Path.home() / ".claude" / "projects"
    if not claude_dir.exists():
        return None
    for jf in claude_dir.rglob(f"{session_id}.jsonl"):
        return str(jf)
    return None


def _parse_transcript_py(session_id: str, path: str):
    """Python-side transcript parser for reimport. Mirrors the Go parser."""
    import json as json_mod

    try:
        with open(path) as f:
            summary_set = False
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    obj = json_mod.loads(line)
                except json_mod.JSONDecodeError:
                    continue

                obj_type = obj.get("type", "")
                uuid = obj.get("uuid", "")
                timestamp = obj.get("timestamp", "")
                message = obj.get("message")

                if obj_type not in ("user", "assistant"):
                    continue
                if not uuid or not message or not isinstance(message, dict):
                    continue

                role = message.get("role", "")
                content = message.get("content", "")

                if obj_type == "user" and role == "user":
                    text = _extract_user_text(content)
                    if not text or text.startswith("<") or text.startswith("{\"tool_use_id\""):
                        continue
                    _insert_message(session_id, "user", text, None, uuid, timestamp)

                elif obj_type == "assistant" and role == "assistant":
                    texts, tools = _extract_assistant_content(content)
                    if texts:
                        _insert_message(session_id, "assistant", texts, None, uuid, timestamp)
                        if not summary_set:
                            _set_summary_if_empty(session_id, texts)
                            summary_set = True
                    for tool in tools:
                        tool_uuid = uuid + ":" + tool["name"]
                        _insert_message(session_id, "tool_use", tool["summary"], tool["name"], tool_uuid, timestamp)

        # Update offset to end of file
        size = os.path.getsize(path)
        db.execute(
            "UPDATE sessions SET transcript_offset = ? WHERE session_id = ?",
            (size, session_id),
        )
    except (OSError, IOError):
        pass


def _extract_user_text(content) -> str:
    if isinstance(content, str):
        return content.strip()
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, dict) and block.get("type") == "text":
                parts.append(block.get("text", ""))
        return "\n".join(parts).strip()
    return ""


def _extract_assistant_content(content) -> tuple[str, list[dict]]:
    if not isinstance(content, list):
        return "", []
    texts = []
    tools = []
    for block in content:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "text" and block.get("text"):
            texts.append(block["text"])
        elif block.get("type") == "tool_use":
            name = block.get("name", "unknown")
            input_str = ""
            if block.get("input"):
                import json as json_mod
                input_str = json_mod.dumps(block["input"])
                if len(input_str) > 500:
                    input_str = input_str[:500] + "..."
            tools.append({
                "name": name,
                "summary": f"{name}: {input_str}" if input_str else name,
            })
    return "\n".join(texts), tools


def _insert_message(session_id, role, content, tool_name, uuid, timestamp):
    if not content:
        return
    db.execute(
        "INSERT OR IGNORE INTO session_messages "
        "(session_id, role, content, tool_name, message_uuid, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?)",
        (session_id, role, content, tool_name, uuid, timestamp),
    )


def _set_summary_if_empty(session_id, text):
    row = db.query(
        "SELECT summary FROM sessions WHERE session_id = ?",
        (session_id,),
    )
    if row and row[0]["summary"]:
        return
    summary = text
    if len(summary) > 200:
        cutoff = 200
        for i in range(cutoff, 100, -1):
            if summary[i] in ".!?":
                cutoff = i + 1
                break
        summary = summary[:cutoff]
    summary = summary.strip()
    db.execute(
        "UPDATE sessions SET summary = ? WHERE session_id = ? AND (summary IS NULL OR summary = '')",
        (summary, session_id),
    )


def _format_ts(ts: str) -> str:
    """Format an ISO timestamp for display."""
    if not ts:
        return ""
    try:
        from datetime import datetime
        dt = datetime.fromisoformat(ts.replace("Z", "+00:00"))
        return dt.strftime("%Y-%m-%d %-I:%M %p").lower()
    except (ValueError, AttributeError):
        return ts[:19]
