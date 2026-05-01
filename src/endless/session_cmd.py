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
    session_value: str | None,
    show_tools: str | None = None,
    show_timestamps: bool = False,
    limit: int = 20,
    sort_asc: bool = False,
    as_json: bool = False,
):
    """Show conversation history for a session.

    With no session_value, defaults to the current session via companion file
    auto-resolution (E-992): in tmux, the sole sibling Claude pane in the
    current window; outside tmux, an explicit id is required.
    """
    if session_value is None:
        project_root = _project_root_for_cwd()
        live = _read_live_companions(project_root / ".endless" / "sessions")
        c = _resolve_companion(None, live, list_hint="endless session list")
        session_value = str(c.get("endless_session_id"))
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
                "AND (m.content LIKE 'Summarize this conversation in 2-3 sentences%'"
                " OR m.content LIKE 'Write a one-line summary of this conversation%')"
                " ORDER BY m.created_at ASC LIMIT 1)"
            )
            # Exclude error/login sessions
            where += (
                " AND (s.summary IS NULL OR s.summary NOT LIKE 'Not logged in%')"
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
        summary = " ".join(summary.split())
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
                + click.style("Run: endless-hook recap", fg="cyan", dim=True)
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
        "Write a one-line summary of this conversation (max 200 chars). "
        "The first 60 characters must identify WHAT was worked on — "
        "a specific feature name, task ID, bug fix, or component. "
        "Examples of good starts: 'Added task search command (E-730)', "
        "'Fixed SQLite migration data loss in sessions table', "
        "'Designed session recap feature with hook-driven capture'. "
        "Examples of BAD starts: 'Let me read the file', "
        "'Discussed various topics', 'Worked on improvements', "
        "'The conversation covered'. "
        "No filler, no preamble. Pure substance.\n\n"
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
    Claude encodes CWD by replacing / with -. Since directory names can
    also contain dashes, we can't decode reliably. Instead, encode each
    registered project path the same way and compare.
    """
    import re
    match = re.search(r'/\.claude/projects/([^/]+)/', jsonl_path)
    if not match:
        return None
    encoded_cwd = match.group(1)

    # Encode each registered project path and find the best match
    rows = db.query("SELECT id, path FROM projects")
    if not rows:
        return None

    best_match = None
    best_len = 0
    for p in rows:
        # Encode project path same way Claude does: / → -
        encoded_proj = p["path"].replace("/", "-")
        # Check if the encoded CWD starts with the encoded project path
        if encoded_cwd == encoded_proj or encoded_cwd.startswith(encoded_proj + "-"):
            # Longest match wins (most specific project)
            if len(encoded_proj) > best_len:
                best_match = p["id"]
                best_len = len(encoded_proj)

    return best_match


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
    # Auto-hide sessions with error summaries
    if summary.startswith("Not logged in") or summary.startswith("Error:"):
        db.execute(
            "UPDATE sessions SET summary = ?, hidden = 1 WHERE session_id = ?",
            (summary, session_id),
        )
        return
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


# --- session cd (E-990) -----------------------------------------------------

def _pid_alive(pid: int) -> bool:
    """Return True if a process with the given pid exists."""
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # Process exists but we can't signal it — counts as alive.
        return True
    except OSError:
        return False
    return True


def _read_live_companions(sessions_dir: Path, harness: str = "claude") -> list[dict]:
    """Read companion files for the given harness, prune stale ones.

    A companion is considered stale if its `pid` is not alive. Stale files
    are unlinked as a side effect (the lazy cleanup E-989 promised).
    """
    if not sessions_dir.is_dir():
        return []
    live: list[dict] = []
    prefix = f"{harness}-"
    for entry in sorted(sessions_dir.iterdir()):
        if not entry.is_file() or not entry.name.startswith(prefix) or not entry.name.endswith(".json"):
            continue
        try:
            data = json_mod.loads(entry.read_text())
        except (OSError, ValueError):
            continue
        pid = int(data.get("pid") or 0)
        if not _pid_alive(pid):
            try:
                entry.unlink()
            except OSError:
                pass
            continue
        data["_path"] = str(entry)
        live.append(data)
    return live


def _project_root_for_cwd() -> Path:
    """Resolve the project root for the current working directory.

    Walks up from cwd looking for a registered project path. Falls back to
    cwd itself if not registered (companion files are still per-project).
    """
    cwd = Path.cwd().resolve()
    candidate = cwd
    while True:
        row = db.query(
            "SELECT path FROM projects WHERE path = ?",
            (str(candidate),),
        )
        if row:
            return Path(row[0]["path"])
        if candidate.parent == candidate:
            break
        candidate = candidate.parent
    return cwd


def _tmux_window_pane_ids() -> list[str] | None:
    """Return pane ids in the current tmux window, or None if not in tmux."""
    if not os.environ.get("TMUX"):
        return None
    import subprocess
    try:
        result = subprocess.run(
            ["tmux", "list-panes", "-F", "#{pane_id}"],
            capture_output=True, text=True, timeout=2,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def _target_path(c: dict) -> str:
    """Return the worktree_path if its directory exists, else cwd.

    The companion file's worktree_path can go stale between hook events
    (a worktree gets removed without firing a refresh). Validating at
    read time means readers fall back to cwd silently rather than
    emitting a path that no longer exists. (E-1037 / E-1038.)
    """
    wt = c.get("worktree_path") or ""
    if wt and os.path.isdir(wt):
        return wt
    return c.get("cwd") or ""


def _format_companion_row(c: dict) -> str:
    eid = c.get("endless_session_id", "")
    pane = c.get("pane_id", "") or "-"
    uuid = (c.get("harness_session_id", "") or "")[:12]
    target = _target_path(c)
    return f"{eid:<5} {pane:<6} {uuid:<14} {target}"


def _resolve_companion(
    session_ref: str | None,
    live: list[dict],
    list_hint: str = "endless session list",
) -> dict:
    """Match a session-ref against live companion records, or auto-resolve
    in tmux. Raises SystemExit(1) with a stderr error on miss/ambiguity/
    no-tmux-no-arg. Returns the matched companion dict on success.
    """
    if session_ref:
        matches = _match_companions(live, session_ref)
        if len(matches) == 1:
            return matches[0]
        if not matches:
            click.echo(
                f"No Claude session matches '{session_ref}'. "
                f"Run `{list_hint}` to see candidates.",
                err=True,
            )
            raise SystemExit(1)
        click.echo(f"Ambiguous: '{session_ref}' matches multiple sessions:", err=True)
        for c in matches:
            click.echo("  " + _format_companion_row(c), err=True)
        raise SystemExit(1)

    # No arg: tmux-sibling auto-resolution.
    window_panes = _tmux_window_pane_ids()
    if window_panes is None:
        click.echo(
            "Outside tmux, an explicit session id is required. "
            f"Run `{list_hint}` to see candidates.",
            err=True,
        )
        raise SystemExit(1)

    my_pane = os.environ.get("TMUX_PANE", "")
    siblings = [
        c for c in live
        if c.get("pane_id") in window_panes and c.get("pane_id") != my_pane
    ]

    if len(siblings) == 1:
        return siblings[0]
    if not siblings:
        click.echo(
            "No sibling Claude pane in this tmux window. "
            f"Run `{list_hint}` to see all candidates project-wide.",
            err=True,
        )
        raise SystemExit(1)
    click.echo("Multiple sibling Claude panes in this window:", err=True)
    for c in siblings:
        click.echo("  " + _format_companion_row(c), err=True)
    click.echo("Specify one by id or UUID prefix.", err=True)
    raise SystemExit(1)


def session_cd_resolve(session_ref: str | None, show_all: bool = False) -> None:
    """Resolve a Claude session to its cwd/worktree and print the path.

    Designed for shell wrapping: `cd "$(endless session cd <id>)"`.
    Success prints just the path on stdout. Errors go to stderr with a
    non-zero exit.
    """
    project_root = _project_root_for_cwd()
    sessions_dir = project_root / ".endless" / "sessions"
    live = _read_live_companions(sessions_dir)

    if show_all:
        if not live:
            click.echo("No live Claude sessions in this project.", err=True)
            raise SystemExit(1)
        click.echo(f"{'ID':<5} {'Pane':<6} {'UUID':<14} CWD")
        for c in live:
            click.echo(_format_companion_row(c))
        return

    c = _resolve_companion(session_ref, live, list_hint="endless session cd --all")
    click.echo(_target_path(c))


def session_show_resolve(session_ref: str | None, as_json: bool = False) -> None:
    """Show details for a Claude session — current by default, or specified by ref.

    Sources the companion record from .endless/sessions/ (E-989) and joins DB
    fields (state, started_at, last_activity, message count, active task) for
    a focused per-session view.
    """
    project_root = _project_root_for_cwd()
    sessions_dir = project_root / ".endless" / "sessions"
    live = _read_live_companions(sessions_dir)
    c = _resolve_companion(session_ref, live, list_hint="endless session list")

    eid = c.get("endless_session_id")
    rows = db.query(
        "SELECT s.state, s.started_at, s.last_activity, s.summary, s.active_task_id, "
        "COALESCE(p.name, '') AS project_name, "
        "(SELECT count(*) FROM session_messages m WHERE m.session_id = s.session_id) AS msg_count "
        "FROM sessions s "
        "LEFT JOIN projects p ON s.project_id = p.id "
        "WHERE s.id = ?",
        (eid,),
    )
    if not rows:
        click.echo(f"Session E-{eid} not found in database.", err=True)
        raise SystemExit(1)
    r = rows[0]

    task_info = None
    if r["active_task_id"]:
        t = db.query(
            "SELECT id, title, status FROM tasks WHERE id = ?",
            (r["active_task_id"],),
        )
        if t:
            task_info = t[0]

    summary = " ".join((r["summary"] or "").split())

    if as_json:
        out = {
            "id": eid,
            "session_id": c.get("harness_session_id"),
            "harness": c.get("harness"),
            "project": r["project_name"],
            "state": r["state"],
            "started_at": r["started_at"],
            "last_activity": r["last_activity"],
            "messages": r["msg_count"],
            "pane_id": c.get("pane_id") or None,
            "cwd": c.get("cwd"),
            "worktree_path": c.get("worktree_path") or None,
            "pid": c.get("pid"),
            "active_task": (
                {"id": task_info["id"], "title": task_info["title"], "status": task_info["status"]}
                if task_info else None
            ),
            "summary": summary,
        }
        click.echo(json_mod.dumps(out, indent=2))
        return

    click.echo()
    click.echo(click.style(f"Session E-{eid}", bold=True))
    click.echo(f"  UUID:          {c.get('harness_session_id', '')}")
    click.echo(f"  Harness:       {c.get('harness', '')}")
    click.echo(f"  Project:       {r['project_name']}")
    click.echo(f"  State:         {r['state']}")
    click.echo(f"  Started:       {r['started_at'] or '-'}")
    click.echo(f"  Last activity: {r['last_activity'] or '-'}")
    click.echo(f"  Messages:      {r['msg_count']}")
    click.echo(f"  Pane:          {c.get('pane_id') or '-'}")
    click.echo(f"  Cwd:           {c.get('cwd', '')}")
    if c.get("worktree_path"):
        click.echo(f"  Worktree:      {c.get('worktree_path')}")
    click.echo(f"  PID:           {c.get('pid')}")
    if task_info:
        click.echo(
            f"  Active task:   E-{task_info['id']} [{task_info['status']}] {task_info['title']}"
        )
    else:
        click.echo("  Active task:   (none)")
    if summary:
        click.echo()
        click.echo(f"  Summary: {summary}")
    click.echo()


# Module-level constant; tests patch this to a smaller value.
_USE_EXTENSION_TIMEOUT_SEC = 5


def session_use_resolve(session_ref: str | None) -> None:
    """Print shell-evaluable activation for a Claude session (E-1014, E-1038).

    Designed for: eval "$(endless session use)"

    Emits a minimal block — cd to the session's worktree (if its directory
    exists) or cwd, plus ENDLESS_SESSION_ID. Other fields (harness, project
    root, etc.) are looked up on demand via 'endless session show <id>
    --json' so they're never stale.

    If a per-project extension script lives at .endless/extensions/use.sh,
    its stdout is appended after the default block. The extension runs with
    ENDLESS_SESSION_ID in its env and a hard 5s timeout. Warnings (security
    refusal, timeout, non-zero exit) go to stderr; the default block is
    always emitted regardless.
    """
    import shlex

    project_root = _project_root_for_cwd()
    sessions_dir = project_root / ".endless" / "sessions"
    live = _read_live_companions(sessions_dir)
    c = _resolve_companion(session_ref, live, list_hint="endless session list")

    eid = c.get("endless_session_id", "")
    target = _target_path(c)  # validated: worktree if dir exists, else cwd

    lines = [
        f"cd {shlex.quote(target)}",
        f"export ENDLESS_SESSION_ID={shlex.quote(str(eid))}",
    ]

    extension = project_root / ".endless" / "extensions" / "use.sh"
    if extension.exists():
        ext_output = _run_use_extension(
            extension,
            {"ENDLESS_SESSION_ID": str(eid)},
        )
        if ext_output:
            lines.append(ext_output.rstrip())

    click.echo("\n".join(lines))


def _run_use_extension(path: Path, extra_env: dict[str, str]) -> str | None:
    """Run a use.sh extension safely. Returns stdout on success/partial,
    None when the script is refused outright. Warnings go to stderr.
    """
    import stat
    import subprocess

    try:
        st = path.stat()
    except OSError as e:
        click.echo(f"warning: cannot stat {path}: {e}", err=True)
        return None

    if not stat.S_ISREG(st.st_mode):
        click.echo(f"warning: ignoring {path}: not a regular file", err=True)
        return None

    if st.st_mode & 0o002:
        click.echo(f"warning: ignoring {path}: world-writable", err=True)
        return None

    if st.st_uid != os.geteuid():
        click.echo(
            f"warning: ignoring {path}: owned by different user (uid {st.st_uid})",
            err=True,
        )
        return None

    full_env = os.environ.copy()
    full_env.update(extra_env)

    try:
        result = subprocess.run(
            ["sh", str(path)],
            env=full_env,
            capture_output=True,
            text=True,
            timeout=_USE_EXTENSION_TIMEOUT_SEC,
        )
    except subprocess.TimeoutExpired:
        click.echo(
            f"warning: {path} timed out after {_USE_EXTENSION_TIMEOUT_SEC}s; skipping",
            err=True,
        )
        return None
    except OSError as e:
        click.echo(f"warning: cannot run {path}: {e}", err=True)
        return None

    if result.stderr:
        # Pass extension's stderr through to the user's terminal as warnings.
        for line in result.stderr.rstrip().split("\n"):
            if line:
                click.echo(f"  [{path.name}] {line}", err=True)

    if result.returncode != 0:
        click.echo(
            f"warning: {path} exited {result.returncode}; using its partial output",
            err=True,
        )

    return result.stdout


def _match_companions(live: list[dict], ref: str) -> list[dict]:
    """Match a session-ref against live companions.

    Numeric ref matches endless_session_id exactly. Otherwise the ref is
    treated as a Claude UUID prefix (case-insensitive).
    """
    if ref.isdigit():
        target = int(ref)
        return [c for c in live if c.get("endless_session_id") == target]
    lo = ref.lower()
    return [
        c for c in live
        if (c.get("harness_session_id", "") or "").lower().startswith(lo)
    ]
