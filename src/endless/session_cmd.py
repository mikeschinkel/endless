"""Session command logic — history, list, search."""

import json as json_mod
import os
import sys
from pathlib import Path

import click

from endless import db, statuses
from endless.project_path import match_project_path, resolved


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
    """Resolve a session by ES-NNN / integer ID, short UUID prefix, or full UUID."""
    # `ES-NNN` is the form `task show` prints under Created:/Touched by: and the
    # form the guide tells sessions to prefer, so accept it wherever a session
    # reference is taken. The prefix is stripped, not looked up separately — it
    # decorates the same integer id.
    if value[:3].upper() == "ES-":
        value = value[3:]
    # Try integer ID first
    try:
        int_id = int(value)
        row = db.query(
            "SELECT id, session_id, project_id, state, summary, "
            "started_at, last_activity, hidden "
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
        "started_at, last_activity, hidden "
        "FROM sessions WHERE session_id = ?",
        (value,),
    )
    if row:
        return dict(row[0])

    # Try UUID prefix match
    row = db.query(
        "SELECT id, session_id, project_id, state, summary, "
        "started_at, last_activity, hidden "
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
    endless_id, session_id (Claude UUID), task_id, worktree_path, state.
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
# of its current status. Done tasks (confirmed/assumed/completed) flip to
# `revisit` — reopening resumes re-evaluation. Still-active (underway/
# unverified) and still-open (untriaged/unplanned/submitted/ready/revisit)
# tasks keep their status; the worktree is just restored under them.
#
# E-1968 gave this set a second job: it is also the set on which `session goto
# --resume` demands an explicit `--revisit` / `--no-revisit`. That is the same
# judgment read two ways — "settled work, where reopening is a real decision" —
# so the two surfaces share one definition rather than drifting apart.
#
# E-1889 narrowed this to task_cmd's `_REOPENABLE_TERMINAL_STATUSES`.
# `declined`/`obsolete` used to be in the set, which made `--reopen` the one
# path that silently revived a deliberate decision not to do the work — the
# other two reopen routes have always refused them. Reviving one is now an
# explicit act (`task update --status revisit`), and `_resolve_resume` refuses
# loudly with that route named.
# E-1891: shares one definition with task_cmd's `_REOPENABLE_TERMINAL_STATUSES`
# by reading the same `reopenable` group, which is what E-1889 said it wanted —
# "the same judgment read two ways" was still two hand-maintained copies.
_REOPEN_TO_REVISIT: frozenset[str] = frozenset(statuses.get("reopenable"))

# Statuses `--reopen` and `--revisit` refuse outright: a decision was made not to
# do the work, so resuming into it must be a deliberate act rather than a side
# effect of recovering a worktree or navigating to a session.
_REOPEN_REFUSED: frozenset[str] = frozenset(statuses.get("reopen-refused"))


def _taskless_resume_description(eid: int) -> str:
    """Describe the container task minted for task-less session `eid` (E-1918).

    Names the tasks the session had already touched, so the placeholder is not
    contentless: those ids are the only evidence on hand of what the session was
    doing, and they are what makes the container findable later. Kept to one
    line and inside `validate_description`'s length cap — long tails are elided
    rather than truncated mid-id.
    """
    base = (
        f"Container task auto-created so session ES-{eid} could be resumed "
        f"into a worktree."
    )
    rows = db.query(
        "SELECT task_id FROM session_tasks WHERE session_id = ? ORDER BY task_id",
        (eid,),
    )
    ids = [f"E-{r['task_id']}" for r in rows]
    if not ids:
        return base + " The session had not touched any tasks."
    shown, extra = ids[:20], max(0, len(ids) - 20)
    tail = f", and {extra} more" if extra else ""
    return base + f" The session had already touched: {', '.join(shown)}{tail}."


def _auto_task_for_taskless_session(target: dict) -> tuple[str, int]:
    """Mint + claim a task for a session that never claimed one (E-1918).

    A no-goal session has nowhere to be resumed into, even though its transcript
    is intact and it did real work. Resuming into the project root was rejected
    (that is the main checkout, which sessions must not edit, with no branch and
    nowhere to commit), and prompting for a title was rejected (someone resuming
    a task-less session is doing so *because* they do not know what it was
    about). So: create the task, claim it, resume into its worktree.

    Idempotency needs no bookkeeping — claiming sets `sessions.task_id`,
    so the next resume of this session takes the ordinary task path and mints
    nothing. tests/tasks/e-1918-verify.sh asserts that rather than trusting it.

    Returns (worktree_path, task_id).
    """
    from endless.task_cmd import create_claimed_task_for_session

    eid = target.get("endless_id")
    uuid = target.get("session_id") or ""
    project_path = target.get("project_path") or ""
    if not project_path:
        raise click.ClickException(
            f"session {eid} belongs to no registered project, so there is "
            f"nowhere to create a task or a worktree for it.\n"
            f"Resume it by hand, without a worktree:\n"
            f"    claude --resume {uuid}"
        )
    root = Path(project_path)
    rows = db.query(
        "SELECT name FROM projects WHERE id = ?", (target.get("project_id"),)
    )
    if not rows:
        raise click.ClickException(
            f"session {eid}'s project (id {target.get('project_id')}) is not in "
            f"the database, so there is nowhere to create a task for it.\n"
            f"Resume it by hand, without a worktree:\n"
            f"    claude --resume {uuid}"
        )

    title = f"Auto-resumed task for session ES-{eid}"
    click.echo(
        click.style("•", fg="cyan")
        + f" session ES-{eid} never claimed a task — creating one to resume into",
        err=True,
    )
    try:
        task_id, wt_path = create_claimed_task_for_session(
            title=title,
            description=_taskless_resume_description(eid),
            project_name=rows[0]["name"],
            project_root=root,
            session_id=eid,
        )
    except click.ClickException as e:
        raise click.ClickException(
            f"could not give session {eid} a task worktree to resume into: "
            f"{e.format_message()}\n"
            f"Resume it by hand, without a worktree:\n"
            f"    claude --resume {uuid}"
        )
    return str(wt_path), task_id


def _resume_decision(
    uuid: str,
    worktree: str,
    label: str,
    eid: int,
    task: int | None,
    decision_out: dict | None,
    **fields,
) -> tuple[str, str, str, int]:
    """Fill `decision_out` with what `--dry-run` prints, and return the target.

    Every resume path funnels through here so the JSON shape is the same on all
    of them (E-1918) — the plain path included, which is what makes the whole
    command testable without exec'ing `claude`. `setdefault` leaves fields an
    earlier step already decided (`_recover_dropped_worktree`'s recovery block).
    """
    if decision_out is not None:
        decision_out.update({
            "uuid": uuid,
            "endless_id": eid,
            "worktree": worktree,
            "label": label,
            "task_id": task,
        })
        decision_out.update(fields)
        decision_out.setdefault("recovered", False)
        decision_out.setdefault("created_task", False)
    return uuid, worktree, label, eid


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

    A session that never claimed a task has no worktree to be resumed into, so
    one is minted for it: a container task, claimed straight to `underway`, with
    the worktree `task claim` would have built (E-1918).

    When a task's worktree is gone (dropped after landing) but the transcript
    survives, behavior depends on `intent` (E-1801):
      - intent is None → raise an error naming `--review`/`--reopen` (the shared
        diagnostic both surfaces get).
      - intent in {"review", "reopen"} → recreate the worktree from the task's
        landing/branch history and return its path. `override` is the explicit
        base ref from `--review=<ref>`/`--reopen=<ref>` (or ".landed"/None for
        the default base chain). `decision_out`, if given, is filled with the
        resolved decision for `--dry-run`.
    """
    target = _resume_target(ref)
    uuid = target.get("session_id") or ""
    worktree = target.get("worktree_path") or ""
    eid = target.get("endless_id")
    task = target.get("task_id")
    label = f"E-{task}" if task else f"session {eid}"

    # E-1889: refuse `--reopen` on a decision-bearing status before anything
    # else. Checked here rather than in `_recover_dropped_worktree` so the
    # refusal is a property of the flag, not of whether the worktree happens
    # to survive. Loud-failure-with-the-route convention, same as the verb,
    # maybe-parent, and db gates.
    task_status = target.get("task_status") or ""
    if intent == "reopen" and task_status in _REOPEN_REFUSED:
        raise click.ClickException(
            f"E-{task} is '{task_status}' — a deliberate decision, not "
            f"dormant work.\n"
            f"Reviving it is an explicit act:\n"
            f"    endless task update E-{task} --status revisit\n"
            f"Then resume without --reopen."
        )

    if not uuid:
        raise click.ClickException(
            f"session {eid} has no Claude UUID to resume "
            "(a background agent that never started?)."
        )

    if worktree and os.path.isdir(worktree):
        return _resume_decision(uuid, worktree, label, eid, task, decision_out)

    # No task at all: mint one, rather than refusing a session whose transcript
    # is intact (E-1918). The old refusal here blamed "a background agent that
    # never started?" — plainly wrong for a 133-message session, and a
    # parenthetical that belongs only on the no-UUID branch above.
    if task is None:
        worktree, task = _auto_task_for_taskless_session(target)
        return _resume_decision(
            uuid, worktree, f"E-{task}", eid, task, decision_out,
            created_task=True,
        )

    # The task's worktree is gone (dropped after landing).
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
    return _resume_decision(uuid, worktree, label, eid, task, decision_out)


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


def _emit_task_status_change(
    task_id: int,
    title: str,
    old_status: str,
    new_status: str,
    session_id,
) -> None:
    """Emit a task status transition on behalf of a session being resumed.

    Shared by `session resume --reopen` (E-1801) and `session goto --resume
    --revisit` (E-1968). Attributing to the RESUMED session rather than the
    current pane keeps the transition correct even when the resume runs from a
    plain recovery shell that has no Claude session of its own.
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

    task_id = int(target["task_id"])
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
        _emit_task_status_change(
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


def _current_pane_task() -> tuple[int, int] | None:
    """(endless_session_id, task_id) for the session running in THIS pane, or
    None when this pane has no session or its session holds no task.

    Backs `session resume`'s clobber gate (E-1968). Best-effort by design: the
    gate exists to stop an accidental replacement of live work, so a pane whose
    session cannot be resolved is treated as empty rather than blocking a
    legitimate recovery from a plain shell — which is exactly where `session
    resume` is most often run.
    """
    from endless.task_cmd import _current_endless_session_id

    try:
        eid = _current_endless_session_id()
    except Exception:
        return None
    if eid is None:
        return None
    rows = db.query("SELECT task_id FROM sessions WHERE id = ?", (eid,))
    if not rows or rows[0]["task_id"] is None:
        return None
    return eid, int(rows[0]["task_id"])


def resume_session(
    ref: str,
    review: str | None = None,
    reopen: str | None = None,
    dry_run: bool = False,
    force: bool = False,
) -> None:
    """Relaunch a lost Claude session in the current tmux pane.

    Resolves `ref` (a task id off the tmux tab, or a session id / Claude UUID)
    to its session UUID and task worktree, cd's into the worktree, and execs
    `claude --resume <uuid>` — replacing this process so the resumed session
    takes over the current pane. This recovers sessions whose panes died in a
    tmux crash: their transcripts and database rows survive the crash intact.

    `--review`/`--reopen` (E-1801) recover a session whose worktree was dropped
    after landing: `--review` rebuilds a detached, read-mostly inspection tree
    (no status change); `--reopen` rebuilds a working branch and, for a
    done/rejected task, flips it to `revisit`. Each accepts an optional base ref
    (`--review=<ref>`); bare, they use `.landed` (the latest landing).

    `--dry-run` stops one line short of the exec: everything the resume needs is
    resolved (and any worktree or container task it implies is created), then the
    `claude --resume` launch is declined and the resolved target is printed as
    JSON instead. E-1918 widened it from the `--review`/`--reopen` paths to
    every path, so the whole command is testable without launching Claude; it
    costs nothing, since the plain path already filled the same dict.
    `--print-decision` is its deprecated spelling, kept working.

    To resume a non-live target in a NEW window instead of clobbering the
    current pane, use `session goto <ref> --resume` (E-1797).

    `--force` (E-1968) is required when the pane this runs in already holds a
    session working a task: the exec replaces that session, and doing it to live
    work should be a decision, not a side effect. `--dry-run` never needs it —
    it does not reach the exec.
    """
    if review is not None and reopen is not None:
        raise click.ClickException(
            "--review and --reopen are mutually exclusive."
        )

    # E-1968: refuse to clobber a pane that holds live work. Checked BEFORE
    # `_resolve_resume`, which can mint a container task and a worktree — a
    # refusal must not leave those behind. Skipped for --dry-run, which stops
    # short of the exec and so replaces nothing.
    if not force and not dry_run:
        held = _current_pane_task()
        if held is not None:
            _eid, held_task = held
            raise click.ClickException(
                f"This pane is working E-{held_task}. `session resume` execs "
                f"in place, so it would replace that session.\n"
                f"  Open the target in a NEW window instead:\n"
                f"      endless session goto {ref} --resume\n"
                f"  Or pass --force to replace this pane."
            )
    intent = "review" if review is not None else "reopen" if reopen is not None else None
    override = review if review is not None else reopen

    decision: dict = {}
    uuid, worktree, label, eid = _resolve_resume(
        ref, intent=intent, override=override, decision_out=decision
    )

    if dry_run:
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
    show_all: bool = False,
    tree: bool = False,
    monitor: bool = False,
    show_hidden: bool = False,
    only_hidden: bool = False,
    as_json: bool = False,
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

    --show-hidden / --only-hidden select how this session's per-session hidden
    tasks (E-1914) are rendered; they are mutually exclusive and rejected here so
    the user gets a click-shaped error rather than the Go flag parser's. --json
    dumps the row set as data instead of drawing it.

    The Go subcommand pins the main DB (sessions live there regardless of cwd),
    so no --config-dir is threaded.
    """
    import shutil
    import subprocess

    if show_hidden and only_hidden:
        raise click.ClickException(
            "--show-hidden and --only-hidden are mutually exclusive."
        )

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
    if show_hidden:
        args.append("--show-hidden")
    if only_hidden:
        args.append("--only-hidden")
    if as_json:
        args.append("--json")
    try:
        result = subprocess.run(args)
    except KeyboardInterrupt:
        return
    if result.returncode != 0:
        raise SystemExit(result.returncode)


# Fixed-width state glyphs for `session list` (E-1914). The old view printed the
# raw state word, and `needs_input` is 11 characters against `idle`'s 4 — so a
# single needs-input row shoved every following column right and the table read as
# ragged. A one-column glyph per state makes the width constant by construction.
#
# ⟳ is `session status`'s own "doing" glyph, reused because it already means
# exactly this: a live session working a task. ▶ is deliberately NOT reused — over
# there it means "ready to spawn", a different claim. The other three states have
# no equivalent in that vocabulary, so they get glyphs of their own; ⏸ was avoided
# for idle because it already means "blocks" in the status view.
SESSION_STATE_ICONS = {
    "working": "⟳",
    "idle": "‖",
    "needs_input": "?",
    "ended": "␥",
}

# Glyph for a state not in the map — a should-never-happen marker, matching the
# ⁇-for-unknown-status idiom in internal/sessionstatuscmd.
SESSION_STATE_UNKNOWN_ICON = "⁇"

# Printed under the table. Static (all four states, always) rather than built from
# the rows present: the legend is short, and a stable legend line means the eye
# learns one mapping instead of re-reading a different one every invocation.
SESSION_STATE_LEGEND = "⟳ working   ‖ idle   ? needs input   ␥ ended"


def _session_state_icon(state: str | None) -> str:
    return SESSION_STATE_ICONS.get(state or "", SESSION_STATE_UNKNOWN_ICON)


def _current_project_name() -> str:
    """The project `session list` defaults to — the one enclosing cwd.

    Sessions are machine-scoped, so the unfiltered list spans every project the
    user has ever worked in; defaulting to the current one is what makes the
    command answer "what is going on HERE". `--all-projects` restores the old
    machine-wide behavior and `--project <name>` picks another, so the error
    below is always recoverable without leaving the directory.
    """
    from endless.task_cmd import _resolve_project

    try:
        _project_id, name = _resolve_project(None)
    except click.ClickException:
        raise click.ClickException(
            "Not in a registered project directory, so there is no current "
            "project to default to.\n"
            "Use 'endless session list --all-projects' for every project, "
            "or '--project <name>' for one."
        ) from None
    return name


def list_sessions(
    project_name: str | None = None,
    all_projects: bool = False,
    show_all: bool = False,
    show_hidden: bool = False,
    show_empty: bool = False,
    state_filter: str | None = None,
    sort_by: str | None = None,
    limit: int = 20,
    as_json: bool = False,
):
    """List recent sessions, defaulting to the project enclosing cwd (E-1914)."""
    # Not a precedence rule: naming a project and asking for all of them are
    # contradictory requests, and silently honoring one would answer a question
    # the user did not ask.
    if project_name and all_projects:
        raise click.ClickException(
            "--project and --all-projects are mutually exclusive."
        )
    if not project_name and not all_projects:
        project_name = _current_project_name()

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

        # A session that never claimed a task (E-1914). Once the discussion
        # Summary column gave way to the active task's id and title, such a row
        # renders as two blank cells — it can no longer say anything. This is not
        # a rare edge either: on a long-lived DB they are the MAJORITY of the
        # roster (384 of 750 at the time of writing), and all but 14 of those had
        # never touched a task at all, so the listing was mostly empty rows.
        #
        # Omitted rather than dropped: --all reveals them, alongside the hidden
        # and empty sessions it already reveals — same kind of noise, same
        # switch. They stay fully addressable by id in the meantime (`session
        # show`, `session goto`), so nothing becomes unreachable.
        where += " AND s.task_id IS NOT NULL"

        if not show_empty:
            # Filter out empty sessions
            where += (
                " AND (SELECT count(*) FROM session_messages m "
                "WHERE m.session_id = s.session_id) > 0"
            )
            # Exclude the throwaway sessions the old recap generator's
            # `claude -p` calls left behind. The generator is gone (E-1906)
            # and stopped creating these before that (E-1470), so this now
            # filters historical rows only — kept because those rows are
            # still in every long-lived DB.
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
        f"s.started_at, s.last_activity, s.hidden, s.task_id, "
        f"COALESCE(t.title, '') as task_title, "
        f"COALESCE(p.name, '') as project_name, "
        f"(SELECT count(*) FROM session_messages m WHERE m.session_id = s.session_id) as msg_count "
        f"FROM sessions s "
        f"LEFT JOIN projects p ON s.project_id = p.id "
        f"LEFT JOIN live_tasks t ON t.id = s.task_id "
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
        # Shape is unchanged apart from the new task_id field. In particular
        # `state` stays the raw word: the glyph substitution is a rendering
        # decision for humans reading a fixed-width table, and encoding it here
        # would force every consumer to learn the icon vocabulary.
        out = [
            {
                "id": r["id"],
                "session_id": r["session_id"][:12],
                "project": r["project_name"],
                "state": r["state"],
                "task_id": r["task_id"],
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

    # The Project column exists only when the output actually spans more than one
    # project; for the single-project case the project name is stated once, in the
    # header, instead of repeated on every row.
    project_names = {r["project_name"] for r in rows}
    multi_project = len(project_names) > 1

    click.echo()
    heading = "Sessions"
    if total_count > len(rows):
        heading += f" ({len(rows)} of {total_count})"
    if not multi_project:
        only = next(iter(project_names))
        if only:
            heading += f" — project: {only}"
    click.echo(click.style(heading, bold=True))

    def task_cell(row) -> str:
        return f"E-{row['task_id']}" if row["task_id"] else ""

    gap = "  "
    id_w = max(4, max(len(str(r["id"])) for r in rows))
    # The state column is exactly one column wide — that is the whole point of the
    # glyphs (see SESSION_STATE_ICONS). ◆ heads it because there is no one-letter
    # word for "state" that would not read as data.
    state_w = 1
    task_w = max(len("Task"), max(len(task_cell(r)) for r in rows))
    msg_w = max(len("Msgs"), max(len(str(r["msg_count"])) for r in rows))
    proj_w = max(len("Project"), max(len(r["project_name"]) for r in rows)) if multi_project else 0

    columns = [("ID", id_w, "<"), ("◆", state_w, "<")]
    if multi_project:
        columns.append(("Project", proj_w, "<"))
    columns += [("Task", task_w, "<"), ("Msgs", msg_w, ">")]

    fixed = sum(w for _, w, _ in columns) + len(gap) * len(columns)
    title_w = max(20, term_width - fixed)

    click.echo(gap.join(f"{name:{align}{w}}" for name, w, align in columns) + gap + "Title")
    click.echo(gap.join("─" * w for _, w, _ in columns) + gap + "─" * title_w)

    for row in rows:
        title = " ".join((row["task_title"] or "").split())
        if len(title) > title_w:
            title = title[:title_w - 1] + "…"
        cells = [f"{row['id']:<{id_w}}", f"{_session_state_icon(row['state']):<{state_w}}"]
        if multi_project:
            cells.append(f"{row['project_name']:<{proj_w}}")
        cells += [f"{task_cell(row):<{task_w}}", f"{row['msg_count']:>{msg_w}}"]
        click.echo(gap.join(cells) + gap + title)

    click.echo()
    click.echo(click.style(SESSION_STATE_LEGEND, dim=True))
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


def _resolve_hide_session(session_value: str | None) -> int:
    """The session a `session hide/unhide --task` applies to (E-1914).

    An explicit positional value wins; otherwise it is the session running the
    command, resolved by the same four-layer lookup every other session-attributed
    CLI call uses. Hiding is per (session, task), so there is no sane fallback
    when no session resolves — a hide with nobody to own it would be a global
    hide, which is the one thing this feature must never be.
    """
    if session_value:
        return _resolve_session(session_value)["id"]

    from endless.task_cmd import _current_endless_session_id

    session_id = _current_endless_session_id()
    if session_id is None:
        raise click.ClickException(
            "No current Endless session, so there is nothing to hide the task "
            "FOR — hiding is per-session.\n"
            "Name the session explicitly: endless session hide ES-<id> --task <task-id>"
        )
    return session_id


def _resolve_hide_tasks(task_refs: list[str]) -> list[int]:
    """Parse and validate `--task` values ('E-500' or '500') into task ids.

    Validated up front, as a set, so a typo in the third id fails the command
    instead of leaving the first two applied.
    """
    task_ids: list[int] = []
    for raw in task_refs:
        ref = raw.strip()
        if ref[:2].upper() == "E-":
            ref = ref[2:]
        try:
            task_id = int(ref)
        except ValueError:
            raise click.ClickException(
                f"Malformed task id '{raw}' (expected E-NNN or NNN)."
            ) from None
        if not db.query("SELECT id FROM live_tasks WHERE id = ?", (task_id,)):
            raise click.ClickException(f"No task found with id E-{task_id}")
        if task_id not in task_ids:
            task_ids.append(task_id)
    return task_ids


def hide_session_tasks(
    session_value: str | None,
    task_refs: list[str],
    unhide: bool = False,
):
    """Hide (or unhide) tasks from ONE session's `session status` listing (E-1914).

    Display-scoped and session-scoped: it changes nothing about the task, and no
    other session's view moves. A hide never expires on its own — no status
    transition clears it, `unverified` included — so this and `--task`-less
    session hiding are the only things that ever write the state.

    Re-hiding an already-hidden task (or unhiding one that is not hidden) is a
    no-op, not an error: the commands are for reaching a desired state, and
    getting there twice should not be a failure.
    """
    from endless.task_cmd import session_id_display, task_id_display

    session_id = _resolve_hide_session(session_value)
    task_ids = _resolve_hide_tasks(task_refs)

    changed: list[int] = []
    for task_id in task_ids:
        if unhide:
            cursor = db.execute(
                "DELETE FROM session_hidden_tasks "
                "WHERE session_id = ? AND task_id = ?",
                (session_id, task_id),
            )
        else:
            cursor = db.execute(
                "INSERT OR IGNORE INTO session_hidden_tasks "
                "(session_id, task_id, hidden_at) "
                "VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%S', 'now'))",
                (session_id, task_id),
            )
        if cursor.rowcount:
            changed.append(task_id)

    verb = "Unhid" if unhide else "Hid"
    already = "not hidden" if unhide else "already hidden"
    session_label = session_id_display(session_id)
    for task_id in task_ids:
        note = "" if task_id in changed else click.style(f" ({already})", dim=True)
        click.echo(
            click.style("•", fg="cyan")
            + f" {verb} {task_id_display(task_id)} for {session_label}{note}"
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
    (state, task_id, last_activity, summary). The `pid` field of
    the old companion record is intentionally absent: liveness is decided
    Go-side, which filters out both ended rows and sessions whose pane was
    observably absent from a tmux server it actually reached (E-1898). A
    session whose server could NOT be reached is still reported, carrying
    `liveness: "unknown"` — unprovable is not the same as gone.

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
            "worktree_path": _worktree_path_for_task(project_root, r.get("task_id")),
            "started_at": r.get("started_at") or "",
            "state": r.get("state"),
            "task_id": r.get("task_id"),
            "last_activity": r.get("last_activity") or "",
            "summary": r.get("summary") or "",
        })
    return live


def _project_root_for_cwd() -> Path:
    """Resolve the project root for the current working directory.

    Walks up from cwd looking for a registered project path. Falls back to
    cwd itself if not registered (companion files are still per-project).

    Both sides of the comparison are normalized (E-2002): cwd on the way in,
    and each candidate row inside match_project_path, so a project reached
    through a symlink resolves to its registered root instead of falling
    through to cwd. The return is the RESOLVED form — this is a directory,
    not the `~/...` the column holds (E-2011).
    """
    cwd = resolved(Path.cwd())
    candidate = cwd
    while True:
        stored_path = match_project_path(candidate)
        if stored_path is not None:
            return resolved(stored_path)
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
    # The column is STORED form; this is a cd target (E-2011).
    return str(resolved(rows[0]["path"])) if rows[0]["path"] else ""


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
        "SELECT s.state, s.started_at, s.last_activity, s.summary, s.task_id, "
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
    if r["task_id"]:
        t = db.query(
            "SELECT id, title, status FROM live_tasks WHERE id = ?",
            (r["task_id"],),
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
            "task": (
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

    An `ES-`/`es-` prefix is stripped first, then a numeric ref matches
    endless_session_id exactly. Otherwise the ref is treated as a Claude UUID
    prefix (case-insensitive).

    Stripping the prefix is what makes `session show`/`cd`/`use ES-NNN` work
    (E-1918): without it the ref skipped the isdigit() branch, fell through to
    the UUID-prefix branch, and matched nothing — so the one form `task show`
    prints and the guide tells sessions to prefer was the one form these three
    commands rejected.
    """
    if ref[:3].upper() == "ES-":
        ref = ref[3:]
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
    cands = [c for c in live if c.get("task_id") == task_id]
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
        task_matches = [c for c in live if c.get("task_id") == n]
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


def _apply_revisit_intent(ref: str, revisit: bool, no_revisit: bool) -> None:
    """Gate and apply `session goto --resume`'s `--revisit` / `--no-revisit`.

    A target whose task is `confirmed` / `assumed` / `completed` is settled
    work. Reopening the session that did it is ambiguous — you might be picking
    the work back up, or you might just be reading it back — and the two mean
    opposite things for the task's status. So E-1968 makes the caller say which:

      --revisit      flip the task to `revisit` and open the session to continue
      --no-revisit   open the session read-only; leave the status alone

    Neither flag is required (or does anything) for a task in any other status:
    nothing about `underway`, `unverified`, `revisit` or the pre-work statuses
    is ambiguous. `declined` / `obsolete` refuse `--revisit` outright, matching
    `session resume --reopen` — reviving a deliberate decision not to do the
    work is an explicit act, not a navigation side effect.
    """
    if revisit and no_revisit:
        raise click.ClickException(
            "--revisit and --no-revisit are mutually exclusive: one reopens "
            "the task, the other leaves its status alone. Pick one."
        )
    target = _try_resume_target(ref)
    if target is None:
        return
    task = target.get("task_id")
    status = target.get("task_status") or ""
    if task is None:
        return

    if revisit and status in _REOPEN_REFUSED:
        raise click.ClickException(
            f"E-{task} is '{status}' — a deliberate decision, not dormant "
            f"work.\n"
            f"Reviving it is an explicit act:\n"
            f"    endless task update E-{task} --status revisit\n"
            f"Then go there with --no-revisit."
        )

    if status not in _REOPEN_TO_REVISIT:
        return

    if not revisit and not no_revisit:
        raise click.ClickException(
            f"E-{task} is '{status}'. Say what you intend:\n"
            f"  --revisit      reopen it and continue work\n"
            f"  --no-revisit   just read the session; leave the status alone"
        )
    if no_revisit:
        return

    # Flipped BEFORE the window opens. If tmux then fails, the task is left in
    # `revisit` — which is right: --revisit is a declaration that this work is
    # being reopened, and that is true whether or not the window came up.
    _emit_task_status_change(
        int(task), target.get("task_title") or "task", status, "revisit",
        session_id=target.get("endless_id"),
    )


def _resume_new_window_pane(
    ref: str, revisit: bool = False, no_revisit: bool = False,
) -> tuple[str, str]:
    """Open a NEW tmux window running `claude --resume <uuid>` in the target's
    worktree (detached, so the caller's own push+switch does the focusing and the
    nav-trail records a single via=goto move) and return (pane, label). Backs
    `session goto --resume` for a non-live target (E-1797). Raises
    click.ClickException if the ref isn't resumable, SystemExit(1) on tmux failure.

    The `--revisit` / `--no-revisit` gate runs FIRST (E-1968), so a target that
    needs an explicit intent is refused before any window is opened.
    """
    import shlex
    _apply_revisit_intent(ref, revisit, no_revisit)
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


def session_goto(
    target_ref: str,
    resume: bool = False,
    revisit: bool = False,
    no_revisit: bool = False,
) -> None:
    """Switch tmux focus to a task's or session's pane, pushing the current pane
    onto the back-stack. See the module section header (E-1681).

    With `resume=True` (`--resume`), a target that resolves but has no live pane
    is relaunched in a new tmux window and focused instead of erroring — the
    manual find-in-DB/open-window/resume dance, automated (E-1797). When the
    target IS live, `--resume` is a no-op and this behaves like plain goto.

    `revisit` / `no_revisit` (E-1968) say what a resume of settled work is FOR;
    see `_apply_revisit_intent`. They belong to the resume path, so like
    `--resume` itself they do nothing when the target is already live — a live
    session's task status is that session's business, not a navigator's.
    """
    if revisit and not resume:
        raise click.ClickException(
            "--revisit applies only with --resume (it says what reopening the "
            "target session is for). A live target is just focused."
        )
    if no_revisit and not resume:
        raise click.ClickException(
            "--no-revisit applies only with --resume (it says what reopening "
            "the target session is for). A live target is just focused."
        )
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
        target_pane, label = _resume_new_window_pane(
            nl.ref, revisit=revisit, no_revisit=no_revisit,
        )

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
