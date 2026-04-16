"""Endless CLI — Click entry point."""

from pathlib import Path

import click

from endless import __version__


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
@click.option("--docs-only", is_flag=True, help="Scan documents only")
def scan(project, docs_only):
    """Scan projects for document changes."""
    from endless.scan import run_scan
    run_scan(project_name=project, docs_only=docs_only)


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


@main.group("plan")
def plan_cmd():
    """Manage project plans."""
    pass


@plan_cmd.command("import")
@click.argument("file", default=None, required=False)
@click.option("--from-claude", is_flag=True,
              help="Import from ~/.claude/plans/")
@click.option("--json", "json_file", default=None,
              help="Import from JSON file")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--replace", is_flag=True,
              help="Replace items from same source file under same parent")
@click.option("--parent", type=int, default=None,
              help="Parent goal ID to import under")
def plan_import(file, from_claude, json_file, project, replace, parent):
    """Import a plan file into the DB."""
    if json_file:
        import json as json_mod
        from pathlib import Path
        from endless.plan_cmd import import_json
        p = Path(json_file).expanduser()
        if not p.exists():
            raise click.ClickException(f"File not found: {p}")
        data = json_mod.loads(p.read_text())
        import_json(data, project_name=project, clear=replace)
    else:
        from endless.plan_cmd import import_plan
        import_plan(
            file_path=file, from_claude=from_claude,
            project_name=project, replace=replace,
            parent_id=parent,
        )


@plan_cmd.command("show")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--all", "show_all", is_flag=True,
              help="Include completed items")
def plan_show(project, show_all):
    """Show the current plan for a project."""
    from endless.plan_cmd import show_plan
    show_plan(project_name=project, show_all=show_all)


@plan_cmd.command("add")
@click.argument("title")
@click.option("--description", default=None,
              help="Longer description of the plan")
@click.option("--phase", default="now",
              help="Phase: now, next, later (default: now)")
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
@click.option("--parent", type=int, default=None,
              help="Parent plan ID to add under")
@click.option("--after", type=int, default=None,
              help="Insert after this plan ID")
def plan_add(title, description, phase, project, parent, after):
    """Add a plan."""
    from endless.plan_cmd import add_item
    add_item(title, description=description, phase=phase,
             project_name=project, after=after, parent_id=parent)


@plan_cmd.command("update")
@click.argument("item_id", type=int)
@click.option("--status", default=None,
              help="Status: needs_plan, ready, in_progress, verify, completed, blocked, revisit")
@click.option("--title", default=None,
              help="New title")
@click.option("--description", default=None,
              help="New description")
@click.option("--text", "text_file", default=None,
              help="Load full plan text from file")
@click.option("--prompt", "prompt_file", default=None,
              help="Load prompt from file")
@click.option("--parent", type=int, default=None,
              help="Set parent plan ID (0 to make root)")
def plan_update(item_id, status, title, description, text_file, prompt_file, parent):
    """Update fields on a plan."""
    from endless.plan_cmd import update_plan
    update_plan(item_id, status=status, title=title,
                description=description, text_file=text_file,
                prompt_file=prompt_file, parent_id=parent)


@plan_cmd.command("remove")
@click.argument("item_id", type=int)
def plan_remove(item_id):
    """Remove a plan item."""
    from endless.plan_cmd import remove_item
    remove_item(item_id)


@plan_cmd.command("complete")
@click.argument("item_id", type=int)
def plan_complete(item_id):
    """Mark a plan item as completed."""
    from endless.plan_cmd import complete_item
    complete_item(item_id)


@plan_cmd.command("start")
@click.argument("item_id", type=int)
def plan_start(item_id):
    """Mark a plan item as in progress."""
    from endless.plan_cmd import start_item
    start_item(item_id)


@plan_cmd.command("detail")
@click.argument("item_id", type=int)
def plan_detail(item_id):
    """Show full detail for a plan item."""
    from endless.plan_cmd import detail_item
    detail_item(item_id)


@plan_cmd.command("prompt")
@click.argument("item_id", type=int)
def plan_prompt(item_id):
    """Output the raw prompt for a plan item (for piping to a session)."""
    from endless.plan_cmd import show_prompt
    show_prompt(item_id)


@plan_cmd.command("spawn")
@click.argument("item_id", type=int)
@click.option("--project", default=None,
              help="Project name (default: detect from cwd)")
def plan_spawn(item_id, project):
    """Spawn a new tmux window with Claude working on a plan's prompt."""
    from endless.plan_cmd import spawn_plan
    spawn_plan(item_id, project_name=project)


@plan_cmd.command("chat")
def plan_chat():
    """Start a chat-only session (no task tracking)."""
    from endless.plan_cmd import start_chat
    start_chat()


@main.command("docs")
@click.argument("name", default=None, required=False)
@click.option("--type", "type_filter", default=None,
              help="Filter by document type")
def docs_cmd(name, type_filter):
    """List tracked documents for a project."""
    from endless.docs_cmd import list_docs
    list_docs(name=name, type_filter=type_filter)


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
