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
def serve(port):
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
    subprocess.run([serve_bin, str(port)])


@main.command("quick-start")
def quick_start():
    """Output the session onboarding guide."""
    from pathlib import Path
    guide = Path(__file__).resolve().parent.parent.parent / "docs" / "guide-2026-04-15-using-endless-in-sessions.md"
    if not guide.exists():
        raise click.ClickException(f"Guide not found at {guide}")
    click.echo(guide.read_text())


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
              help="Include completed items")
@click.option("--status", default=None,
              type=click.Choice(["needs_plan", "ready", "in_progress",
                                 "verify", "completed", "blocked", "revisit", "declined"]),
              help="Filter by status")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Filter by phase")
@click.option("--tier", default=None,
              help="Filter by tier (1-4 or auto/quick/deep/discuss)")
@click.option("--sort", default=None,
              type=click.Choice(["id", "status", "phase", "tier", "created", "title"]),
              help="Sort by column (default: id)")
@click.option("--tree", "as_tree", is_flag=True,
              help="Show as indented tree instead of flat table")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_list(project, show_all, status, phase, tier, sort, as_tree, llm, as_json):
    """List tasks for a project."""
    from endless.task_cmd import show_plan, parse_tier_filter
    tier_val = parse_tier_filter(tier) if tier else None
    show_plan(project_name=project, show_all=show_all,
              status_filter=status, phase_filter=phase,
              tier_filter=tier_val,
              sort_by=sort, tree=as_tree, llm=llm, as_json=as_json)


@task_cmd.command("show")
@click.argument("item_id", type=TASK_ID)
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
def task_show(item_id, no_description, show_text, show_prompt,
              show_children, llm, as_json):
    """Show detail for a specific task."""
    from endless.task_cmd import detail_item
    detail_item(item_id, show_description=not no_description,
                show_text=show_text, show_prompt=show_prompt,
                show_children=show_children, llm=llm, as_json=as_json)


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
def task_next(project, show_all, limit, llm, as_json, tier):
    """Show top actionable tasks, ranked by priority."""
    from endless.task_cmd import next_tasks, parse_tier_filter
    tier_val = parse_tier_filter(tier) if tier else None
    next_tasks(project_name=project, show_all=show_all,
               limit=limit, llm=llm, as_json=as_json, tier=tier_val)


@task_cmd.command("active")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Show tasks from all projects")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_active(project, show_all, llm, as_json):
    """Show in-progress and verify tasks."""
    from endless.task_cmd import active_tasks
    active_tasks(project_name=project, show_all=show_all,
                 llm=llm, as_json=as_json)


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
def task_recent(project, show_all, limit, llm, as_json):
    """Show most recently updated tasks."""
    from endless.task_cmd import recent_tasks
    recent_tasks(project_name=project, show_all=show_all,
                 limit=limit, llm=llm, as_json=as_json)


@task_cmd.command("search")
@click.argument("query")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include completed/declined items")
@click.option("--status", default=None,
              type=click.Choice(["needs_plan", "ready", "in_progress",
                                 "verify", "completed", "blocked", "revisit",
                                 "declined"]),
              help="Filter by status")
@click.option("--phase", default=None,
              type=click.Choice(["now", "next", "later"]),
              help="Filter by phase")
@click.option("--text", "search_text", is_flag=True,
              help="Also search in text field")
@click.option("--prompt", "search_prompt", is_flag=True,
              help="Also search in prompt field")
@click.option("--llm", is_flag=True,
              help="Token-efficient output for LLMs")
@click.option("--json", "as_json", is_flag=True,
              help="JSON output")
def task_search(query, project, show_all, status, phase,
                search_text, search_prompt, llm, as_json):
    """Search tasks by query string."""
    from endless.task_cmd import search_tasks
    search_tasks(query, project_name=project, show_all=show_all,
                 status_filter=status, phase_filter=phase,
                 search_text=search_text, search_prompt=search_prompt,
                 llm=llm, as_json=as_json)


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
              type=click.Choice(["task", "plan", "bug", "research", "spike", "chore"]),
              help="Task type (default: task)")
@click.option("--status", default=None,
              type=click.Choice(["needs_plan", "ready", "in_progress",
                                 "verify", "completed", "blocked", "revisit", "declined"]),
              help="Initial status (default: needs_plan)")
@click.option("--tier", default=None,
              help="Tier (1-4 or auto/quick/deep/discuss)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
def task_add(title, description, phase, project, parent, after, task_type, status, tier, force):
    """Add a task."""
    from endless.task_cmd import add_item, parse_tier
    tier_val = parse_tier(tier) if tier else None
    add_item(title, description=description, phase=phase,
             project_name=project, after=after, parent_id=parent,
             task_type=task_type, status=status, tier=tier_val, force=force)


@task_cmd.command("update")
@click.argument("item_id", type=TASK_ID)
@click.option("--status", default=None,
              help="Status: needs_plan, ready, in_progress, verify, completed, blocked, revisit, declined")
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
              help="Tier (1-4 or auto/quick/deep/discuss, 0 to clear)")
@click.option("--force", is_flag=True,
              help="Bypass title validation")
def task_update(item_id, status, title, description, text_file, prompt_file, parent, phase, tier, force):
    """Update fields on a task."""
    from endless.task_cmd import update_plan, parse_tier
    tier_val = parse_tier(tier) if tier else None
    update_plan(item_id, status=status, title=title,
                description=description, text_file=text_file,
                prompt_file=prompt_file, parent_id=parent,
                phase=phase, tier=tier_val, force=force)


@task_cmd.command("remove")
@click.argument("item_id", type=TASK_ID)
@click.option("--cascade", is_flag=True,
              help="Also remove all descendants")
def task_remove(item_id, cascade):
    """Remove a task."""
    from endless.task_cmd import remove_item
    remove_item(item_id, cascade=cascade)


@task_cmd.command("complete")
@click.argument("item_id", type=TASK_ID)
@click.option("--cascade", is_flag=True,
              help="Also complete all descendants")
def task_complete(item_id, cascade):
    """Mark a task as completed."""
    from endless.task_cmd import complete_item
    complete_item(item_id, cascade=cascade)


@task_cmd.command("start")
@click.argument("item_id", type=TASK_ID)
def task_start(item_id):
    """Mark a task as in progress."""
    from endless.task_cmd import start_item
    start_item(item_id)



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
