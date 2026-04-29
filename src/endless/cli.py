"""Endless CLI — Click entry point."""

from pathlib import Path

import click

from endless import __version__


class TaskIDType(click.ParamType):
    """Click parameter type that accepts task IDs with optional E- prefix."""
    name = "task_id"

    def convert(self, value, param, ctx):
        if isinstance(value, int):
            return value
        s = str(value).strip()
        if s.upper().startswith("E-"):
            s = s[2:]
        try:
            return int(s)
        except ValueError:
            self.fail(f"{value!r} is not a valid task ID (expected integer or E-NNN)", param, ctx)


TASK_ID = TaskIDType()

TASK_STATUSES = ["needs_plan", "ready", "in_progress",
                 "verify", "confirmed", "assumed", "blocked", "revisit", "declined", "obsolete"]


class MultiChoice(click.ParamType):
    """Click parameter type that accepts comma-separated values from a fixed set."""
    name = "multi_choice"

    def __init__(self, choices: list[str]):
        self.choices = choices

    def convert(self, value, param, ctx):
        if value is None:
            return None
        parts = [v.strip() for v in str(value).split(",")]
        invalid = [p for p in parts if p not in self.choices]
        if invalid:
            self.fail(
                f"Invalid value(s): {', '.join(repr(v) for v in invalid)}. "
                f"Choose from: {', '.join(self.choices)}",
                param, ctx,
            )
        return parts


@click.group()
@click.version_option(__version__, prog_name="endless")
def main():
    """Project awareness system for solo developers."""
    pass


@main.command()
@click.argument("path", default=".", type=click.Path(exists=True))
@click.option("--infer", is_flag=True, help="Auto-detect metadata, skip prompts")
@click.option("--name", default=None, help="Project identifier")
@click.option("--label", default=None, help="Display name")
@click.option("--desc", default=None, help="Description")
@click.option("--lang", default=None, help="Primary language")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]))
def register(path, infer, name, label, desc, lang, status):
    """Register a directory as a project."""
    from endless.register import register_project
    register_project(
        Path(path).resolve(),
        name=name, label=label, description=desc,
        language=lang, status=status, infer=infer,
    )


@main.command()
@click.argument("name")
def unregister(name):
    """Unregister a project (preserves .endless config on disk)."""
    from endless.unregister import unregister_project
    unregister_project(name)


@main.command()
@click.argument("name")
def purge(name):
    """Delete .endless/ directory and add to ignore list."""
    from endless.unregister import purge_project
    purge_project(name)


@main.command("set")
@click.argument("expression")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def set_cmd(expression, path):
    """Set a project field. Usage: endless set <name>.<field>=<value>"""
    from endless.set_cmd import set_field
    set_field(expression, path_hint=path)


@main.command()
@click.argument("old_name")
@click.argument("new_name")
@click.option("--path", default=None,
              help="Path segment to disambiguate duplicate names")
def rename(old_name, new_name, path):
    """Rename a project."""
    from endless.rename import rename_project
    rename_project(old_name, new_name, path_hint=path)


@main.command("list")
@click.option("--status", default=None,
              type=click.Choice(["active", "paused", "archived", "idea"]),
              help="Filter by status")
@click.option("--group", is_flag=True, help="Group by group name")
def list_cmd(status, group):
    """List registered projects."""
    from endless.list_cmd import list_projects
    list_projects(status_filter=status, group=group)


@main.command()
@click.argument("name", default=None, required=False)
def status(name):
    """Show detailed status of a project."""
    from endless.status import show_status
    show_status(name)


@main.command()
@click.option("--project", default=None, help="Scan a single project")
def scan(project):
    """Scan and reconcile projects."""
    from endless.scan import run_scan
    run_scan(project_name=project)


@main.command()
@click.option("--port", default=8484, help="Port to serve on")
@click.option("--watch", is_flag=True, help="Auto-restart when the binary is rebuilt")
def serve(port, watch):
    """Start the web dashboard."""
    import shutil
    import subprocess
    serve_bin = shutil.which("endless-serve")
    if not serve_bin:
        raise click.ClickException(
            "endless-serve binary not found on PATH. "
            "Build it: go build -o /usr/local/bin/endless-serve "
            "./cmd/endless-serve/"
        )
    if not watch:
        subprocess.run([serve_bin, str(port)])
        return

    import os
    import signal
    import time

    def _get_mtime(path):
        try:
            return os.stat(path).st_mtime
        except OSError:
            return 0

    last_mtime = _get_mtime(serve_bin)
    proc = None
    try:
        while True:
            click.echo(
                click.style("•", fg="cyan")
                + f" Starting endless-serve (watching {serve_bin} for changes)"
            )
            proc = subprocess.Popen([serve_bin, str(port)])
            while True:
                time.sleep(1)
                if proc.poll() is not None:
                    # Process exited on its own
                    if not watch:
                        return
                    break
                current_mtime = _get_mtime(serve_bin)
                if current_mtime != last_mtime:
                    last_mtime = current_mtime
                    from datetime import datetime
                    ts = datetime.now().strftime("%-I:%M %p").lower()
                    click.echo(
                        click.style("•", fg="yellow")
                        + f" Binary changed, restarting at {ts}..."
                    )
                    proc.terminate()
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
                    break
    except KeyboardInterrupt:
        click.echo("\n" + click.style("•", fg="cyan") + " Shutting down...")
        if proc and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


@main.command("quick-start")
def quick_start():
    """Output the session onboarding guide."""
    from pathlib import Path
    guide = Path(__file__).resolve().parent.parent.parent / "docs" / "guide-2026-04-15-using-endless-in-sessions.md"
    if not guide.exists():
        raise click.ClickException(f"Guide not found at {guide}")
    click.echo(guide.read_text())


@main.group("session")
def session_cmd():
    """View and manage session conversation history."""
    pass


@session_cmd.command("history")
@click.argument("session_id")
@click.option("--tools", "show_tools", default=None, flag_value="truncated",
              help="Include tool calls (truncated)")
@click.option("--tools-full", "show_tools", flag_value="full",
              help="Include full tool call content")
@click.option("--timestamps", is_flag=True,
              help="Show timestamps on each message")
@click.option("--limit", default=20, type=int,
              help="Max messages (default: 20)")
@click.option("--sort", "sort_order", default="desc",
              type=click.Choice(["asc", "desc"]),
              help="Sort order (default: desc, newest first)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_history(session_id, show_tools, timestamps, limit, sort_order, as_json):
    """Show conversation history for a session."""
    from endless.session_cmd import show_history
    show_history(session_id, show_tools=show_tools,
                 show_timestamps=timestamps, limit=limit,
                 sort_asc=(sort_order == "asc"), as_json=as_json)


@session_cmd.command("list")
@click.option("--project", default=None, help="Filter by project")
@click.option("--state", default=None,
              type=click.Choice(["working", "idle", "needs_input", "ended"]),
              help="Filter by state")
@click.option("--sort", "sort_by", default=None,
              type=click.Choice(["id", "project", "state", "count"]),
              help="Sort by column (default: state priority)")
@click.option("--all", "show_all", is_flag=True,
              help="Include hidden and empty sessions")
@click.option("--hidden", "show_hidden", is_flag=True,
              help="Show only hidden sessions")
@click.option("--empty", "show_empty", is_flag=True,
              help="Include empty/short sessions (<=2 messages)")
@click.option("--limit", default=20, type=int,
              help="Max sessions (default: 20)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_list(project, state, sort_by, show_all, show_hidden, show_empty, limit, as_json):
    """List recent sessions."""
    from endless.session_cmd import list_sessions
    list_sessions(project_name=project, show_all=show_all,
                  show_hidden=show_hidden, show_empty=show_empty,
                  state_filter=state, sort_by=sort_by,
                  limit=limit, as_json=as_json)


@session_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None, help="Filter by project")
@click.option("--limit", default=20, type=int,
              help="Max results (default: 20)")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def session_search(query, project, limit, as_json):
    """Search across all session messages."""
    from endless.session_cmd import search_sessions
    search_sessions(query, project_name=project, limit=limit, as_json=as_json)


@session_cmd.command("reimport")
@click.argument("session_id", required=False, default=None)
def session_reimport(session_id):
    """Reimport transcript data from JSONL files."""
    from endless.session_cmd import reimport_sessions
    reimport_sessions(session_value=session_id)


@session_cmd.command("recap")
@click.argument("session_id", required=False, default=None)
@click.option("--force", is_flag=True,
              help="Generate even if < 10 new messages")
def session_recap(session_id, force):
    """Generate recap summaries for sessions using Claude."""
    from endless.session_cmd import recap_session
    recap_session(session_value=session_id, force=force)


@session_cmd.command("hide")
@click.argument("session_ids", nargs=-1, required=True)
def session_hide(session_ids):
    """Hide sessions from the list."""
    from endless.session_cmd import hide_sessions
    hide_sessions(list(session_ids))


@session_cmd.command("unhide")
@click.argument("session_ids", nargs=-1, required=True)
def session_unhide(session_ids):
    """Unhide sessions."""
    from endless.session_cmd import unhide_sessions
    unhide_sessions(list(session_ids))


@main.group("task")
def task_cmd():
    """Manage project tasks."""
    pass


@task_cmd.command("import")
@click.argument("file", default=None, required=False)
@click.option("--from-claude", is_flag=True,
              help="Import from ~/.claude/plans/")
@click.option("--json", "json_file", default=None,
              help="Import from JSON file")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--replace", is_flag=True,
              help="Replace items from same source file under same parent")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent goal ID to import under")
def task_import(file, from_claude, json_file, project, replace, parent):
    """Import a plan file into the DB."""
    if json_file:
        import json as json_mod
        from pathlib import Path
        from endless.task_cmd import import_json
        p = Path(json_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        data = json_mod.loads(p.read_text())
        import_json(data, project_name=project, clear=replace)
    else:
        from endless.task_cmd import import_plan
        import_plan(
            file_path=file, from_claude=from_claude,
            project_name=project, replace=replace,
            parent_id=parent,
        )


@task_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. needs_plan,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Filter by phase")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
@click.option("--related-to", "--relates-to", "related_to_id", type=TASK_ID, default=None,
              help="Filter to tasks related to this task ID")
@click.option("--rel-type", "rel_type", default=None,
              help="Narrow --related-to by relation type (blocks, implements, informs, ...)")
@click.option("--sort", default=None,
              type=click.Choice(["id", "status", "phase", "tier", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--tree", "as_tree", is_flag=True,
              help="Show as indented tree instead of flat table")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_list(project, show_all, status, phase, tier, parent_id, related_to_id, rel_type,
              sort, as_tree, llm, as_json):
    """List tasks for a project."""
    from endless.task_cmd import show_plan, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    show_plan(project_name=project, show_all=show_all,
              status_filter=status, phase_filter=phase,
              tier_filter=tier_val, parent_id=parent_val,
              related_to_id=related_to_id, rel_type=rel_type,
              sort_by=sort, tree=as_tree, llm=llm, as_json=as_json)


@task_cmd.command("show")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--no-description", is_flag=True,
              help="Hide description")
@click.option("--text", "show_text", is_flag=True,
              help="Show text field")
@click.option("--prompt", "show_prompt", is_flag=True,
              help="Show prompt field")
@click.option("--children", "show_children", is_flag=True,
              help="Show direct children")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_show(item_ids, no_description, show_text, show_prompt,
              show_children, llm, as_json):
    """Show detail for one or more tasks."""
    from endless.task_cmd import detail_item
    for item_id in item_ids:
        detail_item(item_id, show_description=not no_description,
                    show_text=show_text, show_prompt=show_prompt,
                    show_children=show_children, llm=llm, as_json=as_json)


task_cmd.add_command(task_show, name="detail")


@task_cmd.command("next")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--limit", default=10, type=int,
              help="Max items to show (default: 10)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
def task_next(project, show_all, limit, llm, as_json, tier, phase, parent_id):
    """Show top actionable tasks, ranked by priority."""
    from endless.task_cmd import next_tasks, parse_tier_filter, parse_parent_filter
    tier_val = parse_tier_filter(tier) if tier else None
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    next_tasks(project_name=project, show_all=show_all,
               limit=limit, llm=llm, as_json=as_json, tier=tier_val,
               phase_filter=phase, parent_id=parent_val)


@task_cmd.command("active")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
def task_active(project, show_all, llm, as_json, parent_id):
    """Show in-progress and verify tasks."""
    from endless.task_cmd import active_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    active_tasks(project_name=project, show_all=show_all,
                 llm=llm, as_json=as_json, parent_id=parent_val)


@task_cmd.command("recent")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--limit", default=10, type=int,
              help="Max items to show (default: 10)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
def task_recent(project, show_all, limit, llm, as_json, parent_id):
    """Show most recently updated tasks."""
    from endless.task_cmd import recent_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    recent_tasks(project_name=project, show_all=show_all,
                 limit=limit, llm=llm, as_json=as_json, parent_id=parent_val)


@task_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include confirmed/assumed/declined items")
@click.option("--status", default=None,
              type=MultiChoice(TASK_STATUSES),
              help="Filter by status (comma-separated, e.g. needs_plan,ready)")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Filter by phase")
@click.option("--parent", "parent_id", default=None,
              help="Filter to children of this task (e.g. E-799), or 'none' for root tasks")
@click.option("--text", "search_text", is_flag=True,
              help="Also search in text field")
@click.option("--prompt", "search_prompt", is_flag=True,
              help="Also search in prompt field")
@click.option("--limit", default=20, type=int,
              help="Max results (default: 20)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_search(query, project, show_all, status, phase, parent_id,
                search_text, search_prompt, limit, llm, as_json):
    """Search tasks by query string."""
    from endless.task_cmd import search_tasks, parse_parent_filter
    parent_val = parse_parent_filter(parent_id) if parent_id else None
    search_tasks(query, project_name=project, show_all=show_all,
                 status_filter=status, phase_filter=phase,
                 parent_id=parent_val,
                 search_text=search_text, search_prompt=search_prompt,
                 limit=limit, llm=llm, as_json=as_json)


@task_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the task")
@click.option("--phase", default="now",
              help="Phase: now, next, later (default: now)")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--parent", type=TASK_ID, default=None,
              help="Parent task ID to add under")
@click.option("--after", type=TASK_ID, default=None,
              help="Insert after this task ID")
@click.option("--type", "task_type", default=None,
              type=click.Choice(["task", "plan", "bug", "research", "spike", "chore", "decision"]),
              help="Task type (default: task)")
@click.option("--status", default=None,
              type=click.Choice(["needs_plan", "ready", "in_progress",
                                 "verify", "confirmed", "assumed", "blocked", "revisit", "declined", "obsolete"]),
              help="Initial status (default: needs_plan)")
@click.option("--tier", default=None,
              help="Tier (1-4 or auto/quick/deep/discuss)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
@click.option("--blocks", "blocks_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this new task blocks (repeatable)")
@click.option("--blocked-by", "blocked_by_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that block this new task (repeatable)")
@click.option("--relates-to", "relates_to_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) related to this new task (repeatable)")
@click.option("--implements", "implements_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that this new task implements (repeatable)")
def task_add(title, description, phase, project, parent, after, task_type, status, tier, force,
             blocks_ids, blocked_by_ids, relates_to_ids, implements_ids):
    """Add a task."""
    from endless.task_cmd import add_item, parse_tier, link_tasks
    tier_val = parse_tier(tier) if tier else None
    new_id = add_item(title, description=description, phase=phase,
                      project_name=project, after=after, parent_id=parent,
                      task_type=task_type, status=status, tier=tier_val, force=force)
    if new_id is None:
        return
    for tid in blocks_ids:
        link_tasks(new_id, tid, "blocks")
    for tid in blocked_by_ids:
        link_tasks(new_id, tid, "blocked_by")
    for tid in relates_to_ids:
        link_tasks(new_id, tid, "relates_to")
    for tid in implements_ids:
        link_tasks(new_id, tid, "implements")


@task_cmd.command("update")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--status", default=None,
              help="Status: needs_plan, ready, in_progress, verify, confirmed, assumed, blocked, revisit, declined, obsolete")
@click.option("--title", default=None,
              help="New title")
@click.option("--description", default=None,
              help="New description")
@click.option("--text", "text_file", default=None,
              help="Load full task text from file")
@click.option("--prompt", "prompt_file", default=None,
              help="Load prompt from file")
@click.option("--parent", type=TASK_ID, default=None,
              help="Set parent task ID (0 to make root)")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Phase: now, next, later")
@click.option("--tier", default=None,
              help="Tier (0=n/a, 1-4 or auto/quick/deep/discuss, none=clear)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
def task_update(item_ids, status, title, description, text_file, prompt_file, parent, phase, tier, force):
    """Update fields on one or more tasks."""
    from endless.task_cmd import update_plan, parse_tier
    tier_val = parse_tier(tier) if tier else None
    for item_id in item_ids:
        update_plan(item_id, status=status, title=title,
                    description=description, text_file=text_file,
                    prompt_file=prompt_file, parent_id=parent,
                    phase=phase, tier=tier_val, force=force)


@task_cmd.command("remove")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also remove all descendants")
def task_remove(item_ids, cascade):
    """Remove one or more tasks."""
    from endless.task_cmd import remove_item
    for item_id in item_ids:
        remove_item(item_id, cascade=cascade)


@task_cmd.group("clear")
def task_clear():
    """Clear a field on a task."""
    pass


@task_clear.command("tier")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
def task_clear_tier(item_ids):
    """Clear tier on one or more tasks (set to NULL/untriaged)."""
    from endless.task_cmd import update_plan, TIER_CLEAR
    for item_id in item_ids:
        update_plan(item_id, tier=TIER_CLEAR)


@task_cmd.command("confirm")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also confirm all descendants")
def task_complete(item_ids, cascade):
    """Confirm one or more tasks."""
    from endless.task_cmd import complete_item
    for item_id in item_ids:
        complete_item(item_id, cascade=cascade)


@task_cmd.command("assume")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--cascade", is_flag=True,
              help="Also assume all descendants")
def task_assume(item_ids, cascade):
    """Assume one or more tasks (believed complete, not yet verified)."""
    from endless.task_cmd import assume_item
    for item_id in item_ids:
        assume_item(item_id, cascade=cascade)


@task_cmd.command("start")
@click.argument("item_id", type=TASK_ID)
def task_start(item_id):
    """Mark a task as in progress."""
    from endless.task_cmd import start_item
    start_item(item_id)



@task_cmd.command("move")
@click.argument("item_id", type=TASK_ID, required=False, default=None)
@click.option("--parent", type=TASK_ID, default=None,
              help="Target parent task ID to move under")
@click.option("--root", is_flag=True,
              help="Move to root (no parent)")
@click.option("--with-children", is_flag=True,
              help="Move the task and all its descendants")
@click.option("--children-of", type=TASK_ID, default=None,
              help="Move children of this task instead")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def task_move(item_id, parent, root, with_children, children_of, project):
    """Move a task to a new parent or to root."""
    from endless.task_cmd import move_task
    move_task(item_id=item_id, parent=parent, root=root,
              with_children=with_children, children_of=children_of,
              project_name=project)


@task_cmd.command("prompt")
@click.argument("item_id", type=TASK_ID)
def task_prompt(item_id):
    """Output the raw prompt for a task (for piping to a session)."""
    from endless.task_cmd import show_prompt
    show_prompt(item_id)


@task_cmd.command("spawn")
@click.argument("item_id", type=TASK_ID)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--no-plan", is_flag=True,
              help="Skip plan mode, send prompt directly")
def task_spawn(item_id, project, no_plan):
    """Spawn a new tmux window with Claude working on a task's prompt."""
    from endless.task_cmd import spawn_plan
    spawn_plan(item_id, project_name=project, no_plan=no_plan)


@task_cmd.command("chat")
def task_chat():
    """Start a chat-only session (no task tracking)."""
    from endless.task_cmd import start_chat
    start_chat()


@task_cmd.command("link")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target_id", type=TASK_ID, required=True,
              help="Target task ID")
@click.option("--as", "dep_type", required=True,
              help="Relation type: blocks, blocked_by, implements, implemented_by, "
                   "informs, informed_by, replaces, replaced_by, relates_to")
def task_link(source_id, target_id, dep_type):
    """Create a typed relation between two tasks."""
    from endless.task_cmd import link_tasks
    link_tasks(source_id, target_id, dep_type)


@task_cmd.command("unlink")
@click.argument("source_id", type=TASK_ID)
@click.option("--to", "target_id", type=TASK_ID, required=True,
              help="Target task ID")
@click.option("--as", "dep_type", default=None,
              help="Relation type to remove (omit to auto-detect when unambiguous)")
def task_unlink(source_id, target_id, dep_type):
    """Remove a typed relation between two tasks."""
    from endless.task_cmd import unlink_tasks
    unlink_tasks(source_id, target_id, dep_type)


@task_cmd.command("block")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID that blocks this task")
def task_block(item_id, blocker_id):
    """Record that a task is blocked by another task. Shortcut for `link --as blocked_by`."""
    from endless.task_cmd import link_tasks
    link_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("replace")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "replacement_id", type=TASK_ID, required=True,
              help="Task ID that replaces this task")
def task_replace(item_id, replacement_id):
    """Mark a task as replaced by another task (sets status to obsolete)."""
    from endless.task_cmd import replace_task
    replace_task(item_id, replacement_id)


@task_cmd.command("unblock")
@click.argument("item_id", type=TASK_ID)
@click.option("--by", "blocker_id", type=TASK_ID, required=True,
              help="Task ID to remove as blocker")
def task_unblock(item_id, blocker_id):
    """Remove a blocking dependency between tasks."""
    from endless.task_cmd import unlink_tasks
    unlink_tasks(item_id, blocker_id, "blocked_by")


@task_cmd.command("deps")
@click.argument("item_id", type=TASK_ID)
def task_deps(item_id):
    """Show all relations for a task. Alias of `task relations`."""
    from endless.task_cmd import show_relations
    show_relations(item_id)


@task_cmd.command("relations")
@click.argument("item_id", type=TASK_ID)
def task_relations(item_id):
    """Show all typed relations for a task, grouped by type."""
    from endless.task_cmd import show_relations
    show_relations(item_id)


@main.command("plan", context_settings={"ignore_unknown_options": True})
@click.argument("args", nargs=-1, type=click.UNPROCESSED)
def plan_redirect(args):
    """Renamed to 'task'. Prints corrected command."""
    corrected = " ".join(["endless", "task"] + list(args))
    click.echo(
        click.style("The 'plan' command has been renamed to 'task'.", fg="yellow")
    )
    click.echo()
    click.echo(f"  Use: {click.style(corrected, bold=True)}")
    click.echo()
    click.echo(
        "If recording a Claude plan (from ~/.claude/plans/), "
        "use --type=plan with 'task add'."
    )
    raise SystemExit(1)


@main.group("decision")
def decision_cmd():
    """Manage project decisions."""
    pass


@decision_cmd.command("list")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show decisions from all projects")
@click.option("--sort", default=None,
              type=click.Choice(["id", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def decision_list(project, show_all, sort, llm, as_json):
    """List decisions for a project."""
    from endless.task_cmd import list_decisions
    list_decisions(project_name=project, show_all=show_all,
                   sort_by=sort, llm=llm, as_json=as_json)


@decision_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the decision")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--about", "about_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) this decision relates to (repeatable; soft link)")
@click.option("--decides", "decides_ids", type=TASK_ID, multiple=True,
              help="Task ID(s) that implement this decision (repeatable; hard link)")
def decision_add(title, description, project, about_ids, decides_ids):
    """Record a decision."""
    if title.lower().startswith("record that "):
        raise click.ClickException(
            "Decision titles should state the decision, not narrate recording it.\n"
            f"  Try: {title[len('record that '):]}"
        )
    from endless.task_cmd import add_item, link_tasks
    new_id = add_item(title, description=description, project_name=project,
                      task_type="decision", status="confirmed", force=True)
    if new_id is None:
        return
    for tid in about_ids:
        link_tasks(new_id, tid, "relates_to")
    for tid in decides_ids:
        # task IMPLEMENTS decision: source=task, target=decision
        link_tasks(tid, new_id, "implements")


@decision_cmd.command("show")
@click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def decision_show(item_ids, llm, as_json):
    """Show detail for one or more decisions."""
    from endless.task_cmd import detail_item
    for item_id in item_ids:
        detail_item(item_id, llm=llm, as_json=as_json)


@main.group("channel")
def channel_cmd():
    """Inter-session messaging. Worker session beacons, human session connects."""
    pass


@channel_cmd.command("beacon")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def channel_beacon(project):
    """Announce this session as available for messaging (run in worker session)."""
    from endless.channel_cmd import beacon
    beacon(project_name=project)


@channel_cmd.command("connect")
@click.argument("channel_id", default=None, required=False)
def channel_connect(channel_id):
    """Connect to a beaconing session (auto-finds if only one beacon)."""
    from endless.channel_cmd import connect
    connect(channel_id)


@channel_cmd.command("send")
@click.argument("message")
def channel_send(message):
    """Send a message to the connected session."""
    from endless.channel_cmd import send
    send(message)


@channel_cmd.command("inbox")
def channel_inbox():
    """Show pending messages."""
    from endless.channel_cmd import inbox
    inbox()


@channel_cmd.command("list")
@click.option("--project", default=None,
              help="Project name")
def channel_list(project):
    """List active beacons."""
    from endless.channel_cmd import list_beacons
    list_beacons(project_name=project)


@channel_cmd.command("close")
def channel_close():
    """Close the active channel."""
    from endless.channel_cmd import close
    close()


@main.group("phrase")
def phrase_cmd():
    """Manage matchers (verbs, pivots, action regexes) in config files."""
    pass


@phrase_cmd.command("add")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None,
              help="Optional scope qualifier (e.g., 'task', 'channel')")
@click.option("--method", default=None,
              type=click.Choice(["exact", "substring", "regex"]),
              help="Match algorithm (default: by type — verb=exact, pivot=substring, others=regex)")
@click.option("--case-sensitive", is_flag=True,
              help="Match exact case (default: case-insensitive)")
@click.option("--machine-only", is_flag=True,
              help="Skip the project config write (machine layer only)")
def phrase_add(type_, value, scope, method, case_sensitive, machine_only):
    """Add a matcher: TYPE VALUE [--scope ...] [--method ...] [--case-sensitive] [--machine-only]."""
    from endless.phrase_cmd import add_phrase
    add_phrase(type_, value, scope, method, case_sensitive, machine_only)


@phrase_cmd.command("list")
@click.option("--type", "type_filter", default=None,
              help="Filter to one type")
@click.option("--scope", "scope_filter", default=None,
              help="Filter to one scope")
@click.option("--all", "show_disabled", is_flag=True,
              help="Include disabled matchers")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def phrase_list(type_filter, scope_filter, show_disabled, as_json):
    """List matchers from project + machine config layers, merged."""
    from endless.phrase_cmd import list_phrases
    list_phrases(type_filter, scope_filter, show_disabled, as_json)


@phrase_cmd.command("disable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Operate on machine layer only")
def phrase_disable(type_, value, scope, machine_only):
    """Disable a matcher value (it stops matching but isn't removed)."""
    from endless.phrase_cmd import disable_phrase
    disable_phrase(type_, value, scope, machine_only)


@phrase_cmd.command("enable")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
def phrase_enable(type_, value, scope):
    """Re-enable a previously disabled matcher value."""
    from endless.phrase_cmd import enable_phrase
    enable_phrase(type_, value, scope)


@phrase_cmd.command("remove")
@click.argument("type_", metavar="TYPE")
@click.argument("value")
@click.option("--scope", default=None, help="Scope qualifier")
@click.option("--machine-only", is_flag=True,
              help="Remove from machine layer only")
def phrase_remove(type_, value, scope, machine_only):
    """Remove a matcher value (use disable for reversible silencing)."""
    from endless.phrase_cmd import remove_phrase
    remove_phrase(type_, value, scope, machine_only)


@main.group("plan-snapshots")
def plan_snapshots_cmd():
    """Inspect plan-file snapshots written by the PostToolUse hook."""
    pass


@plan_snapshots_cmd.command("list")
@click.option("--session", "session_id", default=None,
              help="Filter by session ID (exact match)")
@click.option("--today", is_flag=True, help="Only snapshots written today")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def plan_snapshots_list(session_id, today, as_json):
    """List plan snapshots in the current project."""
    from endless.plan_cmd import list_snapshots
    list_snapshots(session_id=session_id, today_only=today, as_json=as_json)


@plan_snapshots_cmd.command("show")
@click.argument("snap_id")
@click.option("--json", "as_json", is_flag=True, help="JSON output")
def plan_snapshots_show(snap_id, as_json):
    """Show a single plan snapshot's metadata and content."""
    from endless.plan_cmd import show_snapshot
    show_snapshot(snap_id, as_json=as_json)


@main.command("docs")
@click.argument("name", default=None, required=False)
@click.option("--type", "type_filter", default=None,
              help="Filter by document type")
def docs_cmd(name, type_filter):
    """List tracked documents for a project (temporarily disabled)."""
    click.echo(
        click.style("•", fg="yellow")
        + " Document tracking is temporarily disabled."
        + " It will return with shadow git repos."
    )


@main.command("notes")
@click.argument("name", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include resolved notes")
def notes_cmd(name, show_all):
    """Show pending notes for a project."""
    from endless.notes_cmd import list_notes
    list_notes(name=name, show_all=show_all)


@main.group("note")
def note_cmd():
    """Manage notes."""
    pass


@note_cmd.command("add")
@click.argument("message")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def note_add(message, project):
    """Add a note to a project."""
    from endless.notes_cmd import add_note
    add_note(name=project, message=message)


@note_cmd.command("resolve")
@click.argument("note_id", type=int)
def note_resolve(note_id):
    """Mark a note as resolved."""
    from endless.notes_cmd import resolve_note
    resolve_note(note_id=note_id)



@main.command()
@click.argument("path", default=None, required=False)
@click.option("--all", "show_all", is_flag=True,
              help="Include dormant projects in review")
@click.option("--reset", is_flag=True,
              help="Forget prior decisions, re-evaluate all directories")
def discover(path, show_all, reset):
    """Find and register unregistered projects."""
    from endless.discover import run_discover
    run_discover(discover_path=path, show_all=show_all, reset=reset)


@main.group()
def setup():
    """Install hooks and integrations."""
    pass


@setup.command("prompt-hook")
def setup_prompt_hook():
    """Install the ZSH prompt hook for activity monitoring."""
    from endless.setup import setup_prompt_hook
    setup_prompt_hook()


@setup.command("remove-prompt-hook")
def setup_remove_prompt_hook():
    """Remove the ZSH prompt hook."""
    from endless.setup import remove_prompt_hook
    remove_prompt_hook()


@setup.command("claude-hook")
def setup_claude_hook_cmd():
    """Install the Claude Code hook for activity monitoring."""
    from endless.setup import setup_claude_hook
    setup_claude_hook()


@setup.command("remove-claude-hook")
def setup_remove_claude_hook():
    """Remove the Claude Code hook."""
    from endless.setup import remove_claude_hook
    remove_claude_hook()


@setup.command("channel-plugin")
def setup_channel_plugin_cmd():
    """Register the MCP channel plugin for inter-session messaging."""
    from endless.setup import setup_channel_plugin
    setup_channel_plugin()


@setup.command("remove-channel-plugin")
def setup_remove_channel_plugin_cmd():
    """Remove the MCP channel plugin."""
    from endless.setup import remove_channel_plugin
    remove_channel_plugin()


# Suggestions command group (E-918) — AI-agent rule-relaxation suggestions
@main.group("suggestions")
def suggestions_cmd():
    """Review AI-agent suggestions for relaxing enforcement rules."""
    pass


@suggestions_cmd.command("list")
@click.option("--project", default=None, help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True, help="Include accepted suggestions")
@click.option("--source", default=None, help="Filter by source (e.g. drift_detection)")
def suggestions_list(project, show_all, source):
    """List open suggestions (default) or all suggestions with --all."""
    from endless.suggestions_cmd import list_suggestions
    list_suggestions(project, show_all, source)


@suggestions_cmd.command("show")
@click.argument("suggestion_id", type=int)
def suggestions_show(suggestion_id):
    """Show details of a single suggestion."""
    from endless.suggestions_cmd import show_suggestion
    show_suggestion(suggestion_id)


@suggestions_cmd.command("accept")
@click.argument("suggestion_id", type=int)
@click.option("--type", "task_type",
              type=click.Choice(["task", "chore", "bug", "decision", "spike", "research"]),
              default="chore",
              help="Type of task to create (default: chore)")
@click.option("--parent", type=TASK_ID, default=None, help="Parent task ID")
@click.option("--project", default=None, help="Project name (default: from suggestion or cwd)")
def suggestions_accept(suggestion_id, task_type, parent, project):
    """Create a task from a suggestion and link them."""
    from endless.suggestions_cmd import accept_suggestion
    accept_suggestion(suggestion_id, task_type, parent, project)
