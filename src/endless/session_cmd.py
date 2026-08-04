"""Session command logic — history, list, search, reimport."""

import json as json_mod
import os
import sys
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


def _resume_target(ref: str) -> dict:
    """Resolve a task/session ref to its resume target via Go (per E-1486).

    Returns the JSON dict from `endless-go session-query resume-target`:
    endless_id, session_id (Claude UUID), active_task_id, worktree_path, state.
    """
    import shutil
    import subprocess

    from endless import config

    go_bin = shutil.which("endless-go")
    if not go_bin:
        raise click.ClickException("endless-go binary not found on PATH.")
    config.require_db_context()
    try:
        result = subprocess.run(
            [go_bin, *config.go_db_context_args(),
             "session-query", "resume-target", "--ref", ref],
            capture_output=True, text=True, timeout=5,
        )
    except (FileNotFoundError, subprocess.SubprocessError) as e:
        raise click.ClickException(f"endless-go failed: {e}")
    if result.returncode != 0:
        raise click.ClickException(result.stderr.strip() or "endless-go failed")
    try:
        return json_mod.loads(result.stdout)
    except ValueError:
        raise click.ClickException("endless-go returned malformed output")


def _try_resume_target(ref: str) -> dict | None:
    """Best-effort variant of `_resume_target`: return the target dict, or None
    if `ref` doesn't resolve to a resumable session (unknown ref) or the resume
    tooling is unavailable. Lets `session goto`'s not-live error decide whether
    to point the user at `--resume` (E-1797) without raising on an unknown ref.
    """
    try:
        return _resume_target(ref)
    except Exception:
        return None


def _require_claude() -> str:
    """Absolute path to the `claude` binary, or a ClickException if it's absent."""
    import shutil
    claude = shutil.which("claude")
    if not claude:
        raise click.ClickException("`claude` not found on PATH.")
    return claude


# E-1801: `session resume --reopen` transitions the task's status as a function
# of its current status. Done tasks (verified/assumed/completed) and rejected
# tasks (declined/obsolete) flip to `revisit` — reopening resumes re-evaluation.
# Still-active (underway/unverified) and still-open (unplanned/submitted/ready/
# revisit) tasks keep their status; the worktree is just restored under them.
_REOPEN_TO_REVISIT: frozenset[str] = frozenset({
    "confirmed", "assumed", "completed", "declined", "obsolete",
})


def _resolve_resume(
    ref: str,
    *,
    intent: str | None = None,
    override: str | None = None,
    decision_out: dict | None = None,
) -> tuple[str, str, str, int]:
    """Resolve + validate a resume target for `ref`.

    Returns (uuid, worktree, label, endless_id). Raises click.ClickException
    when the ref resolves to a session that can't actually be resumed (no
    harness UUID). Shared by `session resume` (current pane) and `session goto
    --resume` (new window) so both agree on what "resumable" means and emit
    identical diagnostics (E-1797).

    When the worktree is gone (dropped after landing) but the transcript
    survives, behavior depends on `intent` (E-1801):
      - intent is None → raise an error naming `--review`/`--reopen` (the shared
        diagnostic both surfaces get).
      - intent in {"review", "reopen"} → recreate the worktree from the task's
        landing/branch history and return its path. `override` is the explicit
        base ref from `--review=<ref>`/`--reopen=<ref>` (or ".landed"/None for
        the default base chain). `decision_out`, if given, is filled with the
        resolved decision for `--print-decision`.
    """
    target = _resume_target(ref)
    uuid = target.get("session_id") or ""
    worktree = target.get("worktree_path") or ""
    eid = target.get("endless_id")
    task = target.get("active_task_id")
    label = f"E-{task}" if task else f"session {eid}"

    if not uuid:
        raise click.ClickException(
            f"session {eid} has no Claude UUID to resume "
            "(a background agent that never started?)."
        )

    if worktree and os.path.isdir(worktree):
        if decision_out is not None:
            decision_out.update(
                {"recovered": False, "worktree": worktree, "label": label}
            )
        return uuid, worktree, label, eid

    # Worktree is gone (or never mapped to a task).
    if task is None:
        raise click.ClickException(
            f"session {eid} has no task worktree to resume into "
            "(a background agent that never started?)."
        )

    if intent is None:
        raise click.ClickException(
            f"{label}'s worktree is gone (dropped after landing). Recover it "
            f"from the surviving transcript:\n"
            f"  endless session resume {ref} --review   "
            f"inspect the landed result (read-mostly, detached)\n"
            f"  endless session resume {ref} --reopen   "
            f"continue work on it (working branch)"
        )

    worktree = _recover_dropped_worktree(target, intent, override, decision_out)
    return uuid, worktree, label, eid


def _resolve_recovery_base(
    intent: str,
    override: str | None,
    landed_sha: str,
    task_id: int,
    title: str,
    project_root,
) -> str:
    """Resolve the git base a dropped worktree is rebuilt from (E-1801).

    An explicit `override` (`--review=<ref>`/`--reopen=<ref>`, anything but the
    ".landed" sentinel) wins, validated against the repo. Otherwise the default
    chain: the latest landing's `merge_commit_sha` (the `.landed` pseudo-ref),
    else the task's original branch tip if it survives, else a loud error naming
    the explicit-ref escape hatch.
    """
    from endless.worktree_cmd import _branch_exists, _slugify_title, _git_run

    if override and override != ".landed":
        res = _git_run(
            ["rev-parse", "--verify", "--quiet", f"{override}^{{commit}}"],
            cwd=project_root, check=False,
        )
        if res.returncode != 0:
            raise click.ClickException(
                f"base ref {override!r} does not resolve to a commit in this repo."
            )
        return override

    if landed_sha:
        return landed_sha

    branch = f"task/{task_id}-{_slugify_title(title)}"
    if _branch_exists(branch, project_root):
        return branch

    raise click.ClickException(
        f"E-{task_id} never landed and its branch {branch} is gone, so there is "
        f"no base commit to rebuild from. Pass one explicitly: "
        f"`endless session resume E-{task_id} --{intent}=<sha-or-branch>`."
    )


def _emit_recovery_status_change(
    task_id: int,
    title: str,
    old_status: str,
    new_status: str,
    session_id,
) -> None:
    """Emit the `--reopen` status transition, attributed to the reopened
    session (E-1801). Attributing to the resumed session (not the current pane)
    keeps the transition correct even when `session resume` runs from a plain
    recovery shell that has no Claude session of its own.
    """
    from endless.event_bridge import emit_event
    from endless.task_cmd import _resolve_project, _emit_field_changes

    _, proj_name = _resolve_project(None)
    emit_event(
        kind="task.status_changed",
        project=proj_name,
        entity_type="task",
        entity_id=str(task_id),
        payload={
            "old_status": old_status,
            "new_status": new_status,
            "cascade": False,
        },
        session_id=str(session_id) if session_id is not None else None,
    )
    _emit_field_changes(task_id, title, [("status", old_status, new_status)])


def _recover_dropped_worktree(
    target: dict,
    intent: str,
    override: str | None,
    decision_out: dict | None = None,
) -> str:
    """Rebuild a dropped worktree for `session resume --review`/`--reopen`.

    Type-gates epics (refused — a container has no worktree of its own),
    resolves the base commit, recreates the worktree (detached for `--review`,
    a working branch for `--reopen`), and applies `--reopen`'s per-status
    transition. Returns the worktree path.
    """
    from endless.worktree_cmd import recreate_dropped_worktree, _project_root

    task_id = int(target["active_task_id"])
    task_type = target.get("task_type") or ""
    task_status = target.get("task_status") or ""
    title = target.get("task_title") or "task"
    landed_sha = target.get("landed_sha") or ""
    project_root = _project_root()

    if task_type == "epic":
        raise click.ClickException(
            f"E-{task_id} is an epic — a container with no worktree or session "
            f"of its own, so there is nothing to reopen. Pick a child task "
            f"(`endless task show E-{task_id}`) and resume that instead."
        )

    base = _resolve_recovery_base(
        intent, override, landed_sha, task_id, title, project_root
    )

    detached = intent == "review"
    worktree = recreate_dropped_worktree(
        task_id, title, project_root, base, detached=detached
    )

    status_to = None
    if intent == "reopen" and task_status in _REOPEN_TO_REVISIT:
        status_to = "revisit"
        _emit_recovery_status_change(
            task_id, title, task_status, status_to,
            session_id=target.get("endless_id"),
        )

    if decision_out is not None:
        decision_out.update({
            "recovered": True,
            "intent": intent,
            "mode": "detached" if detached else "branch",
            "base": base,
            "worktree": str(worktree),
            "status_from": task_status,
            "status_to": status_to,
        })
    return str(worktree)


def resume_session(
    ref: str,
    review: str | None = None,
    reopen: str | None = None,
    print_decision: bool = False,
) -> None:
    """Relaunch a lost Claude session in the current tmux pane.

    Resolves `ref` (a task id off the tmux tab, or a session id / Claude UUID)
    to its session UUID and task worktree, cd's into the worktree, and execs
    `claude --resume <uuid>` — replacing this process so the resumed session
    takes over the current pane. This recovers sessions whose panes died in a
    tmux crash: their transcripts and ledger rows survive the crash intact.

    `--review`/`--reopen` (E-1801) recover a session whose worktree was dropped
    after landing: `--review` rebuilds a detached, read-mostly inspection tree
    (no status change); `--reopen` rebuilds a working branch and, for a
    done/rejected task, flips it to `revisit`. Each accepts an optional base ref
    (`--review=<ref>`); bare, they use `.landed` (the latest landing). With
    `--print-decision` the recovery is performed but the `claude --resume` launch
    is skipped and the resolved decision is printed as JSON — the seam the verify
    script asserts against.

    To resume a non-live target in a NEW window instead of clobbering the
    current pane, use `session goto <ref> --resume` (E-1797).
    """
    if review is not None and reopen is not None:
        raise click.ClickException(
            "--review and --reopen are mutually exclusive."
        )
    intent = "review" if review is not None else "reopen" if reopen is not None else None
    override = review if review is not None else reopen
    if print_decision and intent is None:
        raise click.ClickException(
            "--print-decision applies only with --review or --reopen."
        )

    decision: dict = {}
    uuid, worktree, label, eid = _resolve_resume(
        ref, intent=intent, override=override, decision_out=decision
    )

    if print_decision:
        click.echo(json_mod.dumps(decision, indent=2))
        return

    claude = _require_claude()

    click.echo(
        f"• Resuming session {eid} ({label}) in {_short_path(worktree)} "
        f"→ claude --resume {uuid[:8]}…",
        err=True,
    )
    os.chdir(worktree)
    sys.stdout.flush()
    sys.stderr.flush()
    os.execvp(claude, ["claude", "--resume", uuid])


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
        live = _live_sessions(project_root)
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


def session_status_resolve(
    show_all: bool = False, tree: bool = False, monitor: bool = False
) -> None:
    """Render the per-session status view (E-1465, renamed E-1688).

    Backs both `endless session status` (one-shot snapshot) and `endless session
    monitor` (the live, looping dashboard, monitor=True). Thin pass-through to
    `endless-go session-status`, which resolves the focal task for the current
    tmux window, gathers the cross-session rows, and renders the
    width/color-aware table itself. We inherit this process's stdout/stderr so
    the Go side detects the real terminal width and color profile; TMUX_PANE is
    inherited via the environment for focal resolution.

    With monitor=True (`--monitor`) the Go side redraws every 2s until
    interrupted; Ctrl-C is the intended exit, so we swallow the KeyboardInterrupt
    (the Go child already restored the cursor on its own SIGINT handler).

    The Go subcommand pins the main DB (sessions live there regardless of cwd),
    so no --config-dir is threaded.
    """
    import shutil
    import subprocess

    go_bin = shutil.which("endless-go")
    if not go_bin:
        raise click.ClickException("endless-go binary not found on PATH.")

    args = [go_bin, "session-status"]
    if show_all:
        args.append("--all")
    if tree:
        args.append("--tree")
    if monitor:
        args.append("--monitor")
    try:
        result = subprocess.run(args)
    except KeyboardInterrupt:
        return
    if result.returncode != 0:
        raise SystemExit(result.returncode)


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
                + click.style("Run: endless-go hook recap", fg="cyan", dim=True)
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
        _generate_recap(session, force=force)
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
        _generate_recap(session, force=False)


def _generate_recap(session: dict, force: bool = False):
    """Generate a recap for a single session."""
    import subprocess
    from endless import internal_claude

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
        # Hook-suppressed (E-1470). Routing through the shared helper sets
        # ENDLESS_NO_HOOKS and disables tools/MCP/persistence, so this headless
        # call no longer registers a session that false-ends the live caller
        # (and writes no throwaway transcript). No --model: recap keeps
        # claude's default model. The session_list filter that hid recap rows
        # now only covers sessions created before this fix.
        result = internal_claude.run_internal_claude(prompt, timeout=60)
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

def _tmux_pane_cwd_map() -> dict[str, str]:
    """Return {pane_id: pane_current_path} for every live tmux pane.

    Used to enrich live-session records with `cwd` on demand (E-1426),
    replacing the companion file's stored `cwd` field. One tmux call
    serves N sessions. Returns an empty dict when tmux is unavailable;
    callers fall back to "" for cwd on miss.

    Tmux-specific by design — non-tmux harnesses (e.g. a future Claude
    started outside tmux with a `pid:N` process value) get no entry and
    therefore an empty cwd. Their own platform helper adds the lookup
    when the time comes.
    """
    import subprocess
    try:
        result = subprocess.run(
            ["tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_current_path}"],
            capture_output=True, text=True, timeout=2,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return {}
    if result.returncode != 0:
        return {}
    out: dict[str, str] = {}
    for line in result.stdout.splitlines():
        parts = line.split(" ", 1)
        if len(parts) == 2 and parts[0]:
            out[parts[0]] = parts[1]
    return out


def _worktree_path_for_task(project_root: Path, task_id: int | None) -> str:
    """Return the absolute worktree path for `task_id`, or "" if none.

    Mirrors the Go helper monitor.WorktreePathForTask: looks for
    <project_root>/.endless/worktrees/e-<task_id> or .../e-<task_id>-*.
    Lexicographically-first match wins; result is "" when nothing exists.
    """
    if not task_id or task_id <= 0:
        return ""
    worktrees_dir = project_root / ".endless" / "worktrees"
    if not worktrees_dir.is_dir():
        return ""
    prefix = f"e-{task_id}"
    candidates: list[Path] = []
    bare = worktrees_dir / prefix
    if bare.is_dir():
        candidates.append(bare)
    for child in worktrees_dir.iterdir():
        if child.name.startswith(prefix + "-") and child.is_dir():
            candidates.append(child)
    if not candidates:
        return ""
    candidates.sort(key=lambda p: p.name)
    return str(candidates[0])


def _live_sessions(project_root: Path, harness: str = "claude") -> list[dict]:
    """Return live Claude sessions for the project, sourced from the DB.

    Replaces `_read_live_companions` (E-1426). Shells out to
    `endless-go session-query list-live` so this Python layer doesn't
    extend the legacy `db.query` pattern (per E-894).

    Each dict carries the fields the rest of this module expects from a
    companion record — endless_session_id, harness_session_id, harness,
    pane_id, cwd, worktree_path, started_at — plus richer DB fields
    (state, active_task_id, last_activity, summary). The `pid` field of
    the old companion record is intentionally absent: liveness is now
    `state != 'ended'` (filtered by the Go side), and crashed-pane
    detection is handled by `ReapDeadTmuxPanes` at SessionStart.

    The `harness` filter currently only accepts "claude"; future
    harnesses would each get their own per-platform list helper.
    """
    import subprocess

    from endless import config
    try:
        result = subprocess.run(
            ["endless-go", *config.go_db_context_args(),
             "session-query", "list-live", "--project-root", str(project_root)],
            capture_output=True, text=True, timeout=5,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return []
    if result.returncode != 0:
        return []
    try:
        raw = json_mod.loads(result.stdout) or []
    except ValueError:
        return []
    if not isinstance(raw, list):
        return []

    pane_cwds = _tmux_pane_cwd_map()
    live: list[dict] = []
    for r in raw:
        if not isinstance(r, dict):
            continue
        if r.get("platform") != harness:
            continue
        pane_id = r.get("pane_id")
        live.append({
            "endless_session_id": r.get("endless_session_id"),
            "harness_session_id": r.get("session_id"),
            "harness": r.get("platform"),
            "pane_id": pane_id or "",
            "cwd": pane_cwds.get(pane_id or "", ""),
            "worktree_path": _worktree_path_for_task(project_root, r.get("active_task_id")),
            "started_at": r.get("started_at") or "",
            "state": r.get("state"),
            "active_task_id": r.get("active_task_id"),
            "last_activity": r.get("last_activity") or "",
            "summary": r.get("summary") or "",
        })
    return live


def _reap_dead_panes(project_root: Path) -> None:
    """Best-effort: end sessions whose owning tmux pane is gone (E-1807).

    Shells to `endless-go session-query reap-dead-panes` — mirroring how
    `_live_sessions` shells to `list-live` — so a ghost owner (a non-ended
    session row whose tmux pane no longer exists, left behind when a session
    died without firing SessionEnd) is flipped to `ended` before the
    spawn/claim ownership guard reads it. The guard's
    `state != 'ended'` query then excludes the just-reaped ghost and the task
    reads as free.

    Silent on every failure: a reaper error must never block a spawn/claim, so
    a missing binary, timeout, or nonzero exit simply falls through to the
    existing ownership behavior.
    """
    import subprocess

    from endless import config
    try:
        subprocess.run(
            ["endless-go", *config.go_db_context_args(),
             "session-query", "reap-dead-panes",
             "--project-root", str(project_root)],
            capture_output=True, text=True, timeout=5,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return


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
    """Return pane ids in the tmux window containing TMUX_PANE, or None
    if not in tmux / TMUX_PANE unset.

    MUST scope with -t $TMUX_PANE. A bare `tmux list-panes` resolves
    against tmux's *currently active* pane in the user's client, not
    the pane of the calling process — so a subprocess spawned from
    pane %150 would see panes for whatever window the user happened to
    be focused on at that instant (E-1395). The bug silently
    misattributed claim/bind/emit_event session resolution to siblings
    in unrelated windows.
    """
    if not os.environ.get("TMUX"):
        return None
    pane = os.environ.get("TMUX_PANE")
    if not pane:
        return None
    import subprocess
    try:
        result = subprocess.run(
            ["tmux", "list-panes", "-t", pane, "-F", "#{pane_id}"],
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


def _short_path(p: str) -> str:
    """Replace the user's home prefix with '~' for display. (E-1049.)

    Display-only; never use the result for actual cd/exec — shells don't
    expand '~' inside single-quoted strings, which is what shlex.quote
    produces for paths with metacharacters.
    """
    if not p:
        return p
    home = os.path.expanduser("~")
    if p == home:
        return "~"
    if p.startswith(home + "/"):
        return "~" + p[len(home):]
    return p


def _emit_resolution_status(c: dict, target_path: str, target: str = "auto") -> None:
    """Emit confirmation status (and warning if applicable) to stderr.

    Silence is wrong when a command produces no visible effect: the user
    can't tell if anything happened. Status goes to stderr so stdout stays
    clean for shell evaluation. (E-1047.)

    Paths are shortened with ~ for display only (E-1049); stdout's cd
    target keeps the full path so shell eval works correctly.

    The stale-worktree warning fires only for target='auto' — that's the
    only mode that silently falls back from worktree to cwd. Explicit
    targets (worktree, cwd, project) either succeed cleanly or surface
    their own error. (E-1050.)
    """
    eid = c.get("endless_session_id", "")
    if target == "auto":
        wt = c.get("worktree_path") or ""
        if wt and not os.path.isdir(wt):
            click.echo(
                f"! worktree {_short_path(wt)} no longer exists; falling back to cwd",
                err=True,
            )
    click.echo(f"• Session {eid} → {_short_path(target_path)}", err=True)


def _session_project_root(c: dict) -> str:
    """Look up the project root for a companion's session via DB. (E-1050.)"""
    eid = c.get("endless_session_id")
    if not eid:
        return ""
    rows = db.query(
        "SELECT p.path FROM sessions s "
        "LEFT JOIN projects p ON s.project_id = p.id "
        "WHERE s.id = ?",
        (eid,),
    )
    if not rows:
        return ""
    return rows[0]["path"] or ""


def _resolve_target(c: dict, target: str) -> str | None:
    """Resolve the cd target for the given --target choice. Returns None
    on error (with the error message already emitted to stderr). (E-1050.)
    """
    if target == "auto":
        return _target_path(c)
    if target == "cwd":
        return c.get("cwd") or ""
    if target == "worktree":
        wt = c.get("worktree_path") or ""
        if not wt:
            click.echo("Session has no worktree bound.", err=True)
            return None
        if not os.path.isdir(wt):
            click.echo(f"Worktree {_short_path(wt)} no longer exists.", err=True)
            return None
        return wt
    if target == "project":
        root = _session_project_root(c)
        if not root:
            click.echo("Could not determine project root for session.", err=True)
            return None
        return root
    click.echo(f"Unknown --target value: {target}", err=True)
    return None


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


def session_cd_resolve(
    session_ref: str | None,
    show_all: bool = False,
    target: str = "auto",
) -> None:
    """Resolve a Claude session to a path and print it.

    --target choices (E-1050):
      auto      worktree if it exists, else cwd (default)
      worktree  worktree only; errors if not bound or missing
      project   project root from DB
      cwd       companion's cwd field as-is

    Designed for shell wrapping: `cd "$(endless session cd <id>)"`.
    Success prints just the path on stdout. Errors go to stderr with a
    non-zero exit.
    """
    project_root = _project_root_for_cwd()
    live = _live_sessions(project_root)

    if show_all:
        if not live:
            click.echo("No live Claude sessions in this project.", err=True)
            raise SystemExit(1)
        click.echo(f"{'ID':<5} {'Pane':<6} {'UUID':<14} CWD")
        for c in live:
            click.echo(_format_companion_row(c))
        return

    # --target project with no explicit session-ref: the project root is a
    # property of cwd, not of any session, so resolve it directly without
    # requiring a session to resolve. This lets `esp` return to the project
    # root even with no active session (e.g. after the Claude session has
    # exited). Equivalent to the tmux-sibling auto-resolution for the
    # in-tmux case, since siblings share the project root. An explicit
    # session-ref still flows through _resolve_companion below so a session
    # in a *different* project resolves to that project's root.
    if target == "project" and session_ref is None:
        click.echo(str(project_root))
        click.echo(
            f"• cwd → {_short_path(str(project_root))} (project root)",
            err=True,
        )
        return

    c = _resolve_companion(session_ref, live, list_hint="endless session cd --all")
    target_path = _resolve_target(c, target)
    if target_path is None:
        raise SystemExit(1)
    click.echo(target_path)
    _emit_resolution_status(c, target_path, target=target)


def session_id_resolve() -> None:
    """Print the current Endless session's integer id for shell substitution.

    Designed for `ENDLESS_SESSION_ID="$(endless session id)"`. Success
    prints just the integer on stdout. Failure writes a one-line
    diagnostic to stderr and exits non-zero; stdout stays empty so the
    command substitution assigns the empty string rather than a
    diagnostic.
    """
    from endless.task_cmd import (
        _current_endless_session_id,
        _find_sibling_claude_session,
    )
    eid = _current_endless_session_id()
    if eid is not None:
        click.echo(eid)
        return

    env_id = os.environ.get("ENDLESS_SESSION_ID")
    pane = os.environ.get("TMUX_PANE")
    if env_id and not env_id.isdigit():
        msg = (
            f"ENDLESS_SESSION_ID={env_id!r} is not an integer; "
            "the resolver only accepts digit strings."
        )
    elif not pane:
        msg = (
            "No current Endless session: not in a tmux pane and "
            "ENDLESS_SESSION_ID is unset. Run from a Claude pane or "
            "export ENDLESS_SESSION_ID=<id>."
        )
    else:
        _, n = _find_sibling_claude_session()
        if n == 0:
            msg = (
                "No current Endless session: this pane is not a Claude "
                "pane and no sibling Claude pane was found in this tmux "
                "window."
            )
        else:
            msg = (
                f"Ambiguous Endless session: {n} sibling Claude panes "
                "in this tmux window. Run `endless session id` from the "
                "intended Claude pane, or export ENDLESS_SESSION_ID=<id>."
            )
    click.echo(msg, err=True)
    raise SystemExit(1)


def session_show_resolve(session_ref: str | None, as_json: bool = False) -> None:
    """Show details for a Claude session — current by default, or specified by ref.

    Sources the live session record from `_live_sessions` (the DB `sessions`
    table joined with the tmux pane map; E-1426 retired the old per-session
    JSON companion files) and joins per-session DB fields (state, started_at,
    last_activity, message count, active task) for a focused per-session view.
    """
    project_root = _project_root_for_cwd()
    live = _live_sessions(project_root)
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
    live = _live_sessions(project_root)
    c = _resolve_companion(session_ref, live, list_hint="endless session list")

    eid = c.get("endless_session_id", "")
    target_path = _target_path(c)  # validated: worktree if dir exists, else cwd

    lines = [
        f"cd {shlex.quote(target_path)}",
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
    _emit_resolution_status(c, target_path)


# Env vars that 'endless session use' is documented to export (E-1038
# reduced this to one). Kept as a constant so 'session forget' stays
# in lockstep — change the contract here, both ends update together.
SESSION_USE_EXPORTED_VARS = ("ENDLESS_SESSION_ID",)


def session_forget_resolve() -> None:
    """Print shell-evaluable lines that unset every var 'session use' sets.

    Designed for: eval "$(endless session forget)"

    Inverse of session_use_resolve. The session itself keeps running;
    only the current shell's pointer to it is cleared. Does not cd
    anywhere — the user can cd back manually or run esp.
    """
    for var in SESSION_USE_EXPORTED_VARS:
        click.echo(f"unset {var}")


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


# ---------------------------------------------------------------------------
# session goto / back — tmux session navigation with a back-stack (E-1681)
#
# `goto` switches the tmux client's focus to a target session's pane; `back`
# returns browser-style via a back-stack. The stack is ephemeral navigation
# state — its tokens resolve to tmux panes, which are only valid for the life of
# the tmux server — so it lives in a tmux server option keyed by the attached
# client (the navigator), NOT in the DB. For a single-terminal setup that one
# client == one global stack.
#
# A stack token is an endless session id (resolved to that session's *current*
# live pane at pop time, so a session that moved to a new pane still works); a
# source location that isn't a tracked session is stored as a raw "%pane" token.
# Capture is `goto`-only, so `back` predictably undoes your last `goto`. The
# durable, captures-everything navigation trail for analysis is a separate
# follow-up (E-1682).
# ---------------------------------------------------------------------------


def _in_tmux() -> bool:
    return bool(os.environ.get("TMUX"))


def _tmux_run(args: list[str], timeout: float = 2.0):
    """Run a tmux command; return the CompletedProcess, or None on failure."""
    import subprocess
    try:
        return subprocess.run(
            ["tmux", *args], capture_output=True, text=True, timeout=timeout,
        )
    except (FileNotFoundError, subprocess.SubprocessError):
        return None


def _pane_exists(pane: str) -> bool:
    """True if `pane` is a currently-existing tmux pane."""
    if not pane:
        return False
    res = _tmux_run(["display-message", "-p", "-t", pane, "#{pane_id}"])
    return bool(res and res.returncode == 0 and res.stdout.strip())


def _tmux_switch_client(pane: str) -> bool:
    """Switch the tmux client to `pane`'s window/session. True on success."""
    res = _tmux_run(["switch-client", "-t", pane])
    return bool(res and res.returncode == 0)


# One-shot tmux server option the nav-trail recorder reads to tag a goto-driven
# focus change (E-1682). Kept in sync with navViaOption in
# internal/tmuxcmd/record_nav.go.
_NAV_VIA_OPTION = "@endless_nav_via"


def _set_nav_via_goto() -> None:
    """Mark the next focus change as via=goto for the nav-trail recorder."""
    _tmux_run(["set-option", "-g", _NAV_VIA_OPTION, "goto"])


def _clear_nav_via() -> None:
    """Unset the via marker (used when a goto switch did not actually happen)."""
    _tmux_run(["set-option", "-gu", _NAV_VIA_OPTION])


def _backstack_key() -> str:
    """tmux server-option name holding the current client's back-stack.

    Scoped to the navigator = the attached tmux client (a tty path), sanitized
    to option-safe chars. An unresolvable client falls back to a shared key, so
    a single-terminal setup uses one stack either way.
    """
    res = _tmux_run(["display-message", "-p", "#{client_name}"])
    client = res.stdout.strip() if res and res.returncode == 0 else ""
    if not client:
        return "@endless_backstack"
    import re
    return "@endless_backstack_" + re.sub(r"[^A-Za-z0-9]", "_", client)


def _backstack_read(key: str) -> list[str]:
    res = _tmux_run(["show-options", "-gqv", key])
    if not res or res.returncode != 0:
        return []
    return [t for t in res.stdout.strip().split() if t]


def _backstack_write(key: str, stack: list[str]) -> None:
    _tmux_run(["set-option", "-g", key, " ".join(stack)])


def _backstack_push(key: str, token: str) -> None:
    stack = _backstack_read(key)
    stack.append(token)
    _backstack_write(key, stack)


def _backstack_pop(key: str) -> str | None:
    stack = _backstack_read(key)
    if not stack:
        return None
    top = stack.pop()
    _backstack_write(key, stack)
    return top


def _current_pane_token(live: list[dict]) -> str | None:
    """Token for the current pane to push onto the stack: its endless session id
    if the pane is a tracked session, else the raw pane id. None if no pane.
    """
    pane = os.environ.get("TMUX_PANE", "")
    if not pane:
        return None
    for c in live:
        if c.get("pane_id") == pane:
            eid = c.get("endless_session_id")
            if eid:
                return str(eid)
    return pane


def _resolve_session_token_to_pane(live: list[dict], token: str) -> str | None:
    """Resolve a back-stack token to a currently-live tmux pane, or None.

    A "%..." token is a raw pane id, used directly if it still exists. A numeric
    token is an endless session id, resolved to that session's current pane.
    """
    if token.startswith("%"):
        return token if _pane_exists(token) else None
    if token.isdigit():
        sid = int(token)
        for c in live:
            if c.get("endless_session_id") == sid:
                pane = c.get("pane_id") or ""
                return pane if _pane_exists(pane) else None
        return None
    return None


def _token_label(token: str, pane: str) -> str:
    if token.startswith("%"):
        return f"pane {pane}"
    return f"session {token} (pane {pane})"


class _GotoNotLive(Exception):
    """Raised by the goto resolvers when `ref` names a single target that has no
    LIVE, reachable pane (as opposed to being ambiguous or malformed). This is
    exactly the case `session goto --resume` can recover by relaunching in a new
    window, and the case whose error should point the user at `--resume` when the
    target is resumable (E-1797). Carries the ref (for the --resume hint and the
    resumable check) and the base error line (sans the trailing next-step hint,
    which `session_goto` appends based on whether the ref is resumable).
    """

    def __init__(self, ref: str, base: str):
        super().__init__(base)
        self.ref = ref
        self.base = base


def _goto_task(task_id: int, live: list[dict]) -> tuple[str, str]:
    """Resolve a task id to (pane, label): the most-recently-active live session
    working it. Raises _GotoNotLive if none is reachable.
    """
    from endless.task_cmd import task_id_display
    disp = task_id_display(task_id)
    cands = [c for c in live if c.get("active_task_id") == task_id]
    cands.sort(key=lambda c: c.get("last_activity") or "", reverse=True)
    for c in cands:
        pane = c.get("pane_id") or ""
        if pane and _pane_exists(pane):
            eid = c.get("endless_session_id", "")
            return pane, f"{disp} → session {eid} (pane {pane})"
    if cands:
        raise _GotoNotLive(
            disp, f"The live session on {disp} has no reachable tmux pane."
        )
    raise _GotoNotLive(disp, f"No live session on {disp}.")


def _goto_session(matches: list[dict], ref: str) -> tuple[str, str]:
    """Resolve session matches to (pane, label). Raises _GotoNotLive on
    no-match / unreachable pane; SystemExit(1) on ambiguity.
    """
    if not matches:
        raise _GotoNotLive(ref, f"No live session matches '{ref}'.")
    if len(matches) > 1:
        click.echo(f"Ambiguous: '{ref}' matches multiple sessions:", err=True)
        for c in matches:
            click.echo("  " + _format_companion_row(c), err=True)
        raise SystemExit(1)
    c = matches[0]
    eid = c.get("endless_session_id", "")
    pane = c.get("pane_id") or ""
    if not pane or not _pane_exists(pane):
        raise _GotoNotLive(ref, f"Session {eid} has no reachable tmux pane.")
    return pane, f"session {eid} (pane {pane})"


def _resolve_goto_target(ref: str, live: list[dict]) -> tuple[str, str]:
    """Resolve a goto ref to (target_pane, label).

    Forms: `ES-NNNN` -> session; `E-NNNN` -> task; bare `NNNN` -> a session id or
    a task id (errors if it matches both); `<uuid-prefix>` -> session. Raises
    _GotoNotLive when a single target resolves but has no live pane (recoverable
    by `--resume`), or SystemExit(1) with a stderr error on ambiguity.
    """
    from endless.task_cmd import task_id_display
    raw = ref.strip()

    # `ES-NNNN` (E-1261) names a session unambiguously, so it goes straight to
    # the session resolver and never hits the bare-integer ambiguity check below
    # — stating the id space is the whole point of the prefix. It is what
    # `task show`'s Created:/Touched by: block prints (E-1866). Resolution
    # continues on the bare digits so the not-live/--resume path downstream sees
    # the same ref shape a bare-integer goto produces.
    if raw.upper().startswith("ES-") and raw[3:].isdigit():
        digits = raw[3:]
        return _goto_session(_match_companions(live, digits), digits)

    if raw.upper().startswith("E-") and raw[2:].isdigit():
        return _goto_task(int(raw[2:]), live)

    if raw.isdigit():
        n = int(raw)
        sess_matches = [c for c in live if c.get("endless_session_id") == n]
        task_matches = [c for c in live if c.get("active_task_id") == n]
        if sess_matches and task_matches:
            click.echo(
                f"'{raw}' is ambiguous: it matches both session {n} and the "
                f"live session on task {task_id_display(n)}. "
                f"Write 'E-{n}' for the task.", err=True,
            )
            raise SystemExit(1)
        if sess_matches:
            return _goto_session(sess_matches, raw)
        if task_matches:
            return _goto_task(n, live)
        raise _GotoNotLive(
            raw, f"No live session matches '{raw}' (as a session id or a task)."
        )

    return _goto_session(_match_companions(live, raw), raw)


def _spawner_pane(live: list[dict]) -> tuple[str, str] | None:
    """Resolve this window's `@endless_spawned_by` to the spawner's current live
    pane, for `back`'s empty-stack fallback. Returns (pane, label) or None.
    """
    pane = os.environ.get("TMUX_PANE", "")
    target = ["-t", pane] if pane else []
    res = _tmux_run(["display-message", "-p", *target, "#{@endless_spawned_by}"])
    if not res or res.returncode != 0:
        return None
    val = res.stdout.strip()
    if not val.isdigit():  # unset, or a "pid-NNN" fallback spawner id
        return None
    resolved = _resolve_session_token_to_pane(live, val)
    if not resolved:
        return None
    return resolved, f"spawning session {val} (pane {resolved})"


def _resume_new_window_pane(ref: str) -> tuple[str, str]:
    """Open a NEW tmux window running `claude --resume <uuid>` in the target's
    worktree (detached, so the caller's own push+switch does the focusing and the
    nav-trail records a single via=goto move) and return (pane, label). Backs
    `session goto --resume` for a non-live target (E-1797). Raises
    click.ClickException if the ref isn't resumable, SystemExit(1) on tmux failure.
    """
    import shlex
    uuid, worktree, rlabel, _eid = _resolve_resume(ref)
    claude = _require_claude()
    cmd = f"{shlex.quote(claude)} --resume {shlex.quote(uuid)}"
    res = _tmux_run(
        ["new-window", "-d", "-c", worktree, "-P", "-F", "#{pane_id}", cmd]
    )
    if not res or res.returncode != 0 or not res.stdout.strip():
        click.echo("Could not open a new tmux window to resume.", err=True)
        raise SystemExit(1)
    pane = res.stdout.strip()
    return pane, f"--resume {rlabel} (new window)"


def _fail_not_live(nl: _GotoNotLive) -> None:
    """Print `nl`'s not-live error and exit. When the ref is resumable, point the
    user at `--resume`; otherwise fall back to the `session list` hint (E-1797)."""
    if _try_resume_target(nl.ref) is not None:
        click.echo(
            f"{nl.base} It isn't live — run "
            f"`endless session goto {nl.ref} --resume` "
            f"to resume it in a new window.",
            err=True,
        )
    else:
        click.echo(
            f"{nl.base} Run `endless session list` to see candidates.",
            err=True,
        )
    raise SystemExit(1)


def session_goto(target_ref: str, resume: bool = False) -> None:
    """Switch tmux focus to a task's or session's pane, pushing the current pane
    onto the back-stack. See the module section header (E-1681).

    With `resume=True` (`--resume`), a target that resolves but has no live pane
    is relaunched in a new tmux window and focused instead of erroring — the
    manual find-in-DB/open-window/resume dance, automated (E-1797). When the
    target IS live, `--resume` is a no-op and this behaves like plain goto.
    """
    if not _in_tmux():
        click.echo(
            "session goto requires tmux (no $TMUX in this environment).",
            err=True,
        )
        raise SystemExit(1)
    live = _live_sessions(_project_root_for_cwd())
    try:
        target_pane, label = _resolve_goto_target(target_ref, live)
    except _GotoNotLive as nl:
        if not resume:
            _fail_not_live(nl)
        target_pane, label = _resume_new_window_pane(nl.ref)

    key = _backstack_key()
    token = _current_pane_token(live)
    if token:
        _backstack_push(key, token)
    # Tag the focus change the switch is about to cause as via=goto. The global
    # focus-change hook's recorder reads + clears this one-shot marker (E-1682);
    # every other (manual) move records as via=manual. Set immediately before
    # the switch so a concurrent manual move can't trip it.
    _set_nav_via_goto()
    if not _tmux_switch_client(target_pane):
        if token:  # target closed between resolution and switch — undo the push
            _backstack_pop(key)
        _clear_nav_via()  # no switch happened; don't mis-tag the next move
        click.echo(
            f"Could not switch to pane {target_pane} (it may have closed).",
            err=True,
        )
        raise SystemExit(1)
    click.echo(f"• goto {label}", err=True)


def session_back() -> None:
    """Return to the previous session via the back-stack, browser-style. Skips
    panes/sessions that have since closed; falls back to the spawning session
    when the stack is empty. See the module section header (E-1681).
    """
    if not _in_tmux():
        click.echo(
            "session back requires tmux (no $TMUX in this environment).",
            err=True,
        )
        raise SystemExit(1)
    live = _live_sessions(_project_root_for_cwd())
    key = _backstack_key()

    while True:
        token = _backstack_pop(key)
        if token is None:
            break
        pane = _resolve_session_token_to_pane(live, token)
        if pane and _tmux_switch_client(pane):
            click.echo(f"• back → {_token_label(token, pane)}", err=True)
            return
        # Stale or unswitchable token: it's already popped, so drop and retry.

    spawner = _spawner_pane(live)
    if spawner:
        pane, label = spawner
        if _tmux_switch_client(pane):
            click.echo(f"• back → {label}", err=True)
            return

    click.echo("no previous session", err=True)
    raise SystemExit(1)


def _resolve_client_name() -> str:
    """The attached tmux client_name (a tty path), or '' if unresolvable.

    The nav-trail recorder keys rows by this same value (#{client_name}), so it
    scopes `session trail` to the navigator whose moves are being viewed.
    """
    res = _tmux_run(["display-message", "-p", "#{client_name}"])
    return res.stdout.strip() if res and res.returncode == 0 else ""


def _nav_endpoint_label(session_id, task_id, pane: str | None) -> str:
    """Render one navigation endpoint: a tracked session (with its task id) or
    the raw pane id for an untracked location.
    """
    from endless.task_cmd import task_id_display
    if session_id:
        if task_id:
            return f"session {session_id} ({task_id_display(task_id)})"
        return f"session {session_id}"
    if pane:
        return f"pane {pane}"
    return "—"


def session_trail(show_all: bool = False, limit: int = 50) -> None:
    """Print the durable session-navigation trail, newest-first (E-1682).

    Each edge shows `from → to` (session id + task id, or raw pane), the `via`
    tag (manual / goto), and a relative time. Defaults to the current tmux
    client; --all lists every client's moves. The DB read goes through
    `endless-go session-query trail` (no Python DB read, per E-1486).
    """
    import subprocess

    from endless import config
    from endless.task_cmd import _format_relative

    cmd = ["endless-go", *config.go_db_context_args(),
           "session-query", "trail", "--limit", str(limit)]
    if not show_all:
        client = _resolve_client_name()
        # An unresolved client (not attached to a tmux client) scopes to the
        # empty-string client key, which matches nothing — clearer than silently
        # widening to every client. Suggest --all in the empty-state below.
        cmd += ["--client", client]

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
    except (FileNotFoundError, subprocess.SubprocessError):
        click.echo("Could not read the navigation trail.", err=True)
        raise SystemExit(1)
    if result.returncode != 0:
        click.echo(result.stderr.strip() or "Could not read the navigation trail.",
                   err=True)
        raise SystemExit(1)
    try:
        edges = json_mod.loads(result.stdout) or []
    except ValueError:
        edges = []

    if not edges:
        scope = "any client" if show_all else "this client"
        click.echo(f"No navigation recorded yet for {scope}.", err=True)
        if not show_all:
            click.echo("Try `endless session trail --all`.", err=True)
        return

    for e in edges:
        frm = _nav_endpoint_label(
            e.get("from_session_id"), e.get("from_task_id"), e.get("from_pane"))
        to = _nav_endpoint_label(
            e.get("to_session_id"), e.get("to_task_id"), e.get("to_pane"))
        via = e.get("via") or "manual"
        when = _format_relative(e.get("created_at"))
        prefix = f"[{e.get('client')}] " if show_all else ""
        click.echo(f"• {prefix}{frm} → {to}  ({via}, {when})")
        summary = (e.get("to_summary") or "").strip()
        if summary:
            click.echo(f"    {summary[:100]}")
