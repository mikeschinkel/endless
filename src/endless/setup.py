"""Setup commands — install hooks into shell/tool configs."""

import json
import shutil
from pathlib import Path

import click

HOOK_COMMENT = "# Endless: project activity monitor (prompt hook)"
HOOK_CODE = """\
_endless_prompt_hook() {
  (endless-hook prompt "$PWD" &>/dev/null &)
}
precmd_functions+=(_endless_prompt_hook)"""

HOOK_BLOCK = f"{HOOK_COMMENT}\n{HOOK_CODE}"

DEFAULT_ZSHRC = Path.home() / ".zshrc"


def _find_endless_hook() -> str | None:
    """Check if endless-hook binary is on PATH."""
    return shutil.which("endless-hook")


def _file_contains_hook(path: Path) -> bool:
    """Check if a file already contains the Endless prompt hook."""
    if not path.exists():
        return False
    content = path.read_text()
    return "_endless_prompt_hook" in content


def setup_prompt_hook():
    # Check binary exists
    hook_bin = _find_endless_hook()
    if not hook_bin:
        raise click.ClickException(
            "endless-hook binary not found on PATH. "
            "Build it first: go build -o /usr/local/bin/endless-hook "
            "./cmd/endless-hook/"
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Found endless-hook at {hook_bin}"
    )

    # Ask about zshrc location
    click.echo()
    click.echo(
        "The prompt hook needs to be added to your ZSH "
        "startup file."
    )
    click.echo(
        f"The standard location is "
        + click.style(str(DEFAULT_ZSHRC), bold=True)
    )
    click.echo()

    is_standard = click.confirm(
        f"Is {DEFAULT_ZSHRC} where we should add it?",
        default=True,
    )

    if is_standard:
        target = DEFAULT_ZSHRC
    else:
        click.echo()
        click.echo("Enter the path to your ZSH startup file:")
        target_str = click.prompt("Path")
        target = Path(target_str).expanduser()
        if not target.exists():
            raise click.ClickException(
                f"File not found: {target}"
            )

    # Check if already installed
    if _file_contains_hook(target):
        click.echo(
            click.style("•", fg="cyan")
            + " Prompt hook is already installed in "
            + click.style(str(target), bold=True)
        )
        return

    # Show what we'll add
    click.echo()
    click.echo("The following will be added to "
               + click.style(str(target), bold=True) + ":")
    click.echo()
    for line in HOOK_BLOCK.splitlines():
        click.echo(f"  {click.style(line, fg='green')}")
    click.echo()

    if not click.confirm("Proceed?", default=True):
        # Show manual instructions instead
        click.echo()
        click.echo("To install manually, add this to your "
                    "ZSH startup file:")
        click.echo()
        for line in HOOK_BLOCK.splitlines():
            click.echo(f"  {line}")
        click.echo()
        return

    # Append to file
    content = target.read_text()
    if not content.endswith("\n"):
        content += "\n"
    content += f"\n{HOOK_BLOCK}\n"
    target.write_text(content)

    click.echo(
        click.style("•", fg="cyan")
        + " Prompt hook installed in "
        + click.style(str(target), bold=True)
    )
    click.echo(
        click.style("•", fg="cyan")
        + " Run "
        + click.style(f"source {target}", bold=True)
        + " or open a new terminal to activate."
    )


def remove_prompt_hook():
    """Remove the Endless prompt hook from a ZSH startup file."""
    click.echo(
        "Enter the path to the file containing "
        "the Endless prompt hook:"
    )
    target_str = click.prompt(
        "Path", default=str(DEFAULT_ZSHRC),
    )
    target = Path(target_str).expanduser()

    if not target.exists():
        raise click.ClickException(f"File not found: {target}")

    if not _file_contains_hook(target):
        click.echo(
            click.style("•", fg="cyan")
            + " No Endless prompt hook found in "
            + click.style(str(target), bold=True)
        )
        return

    content = target.read_text()
    # Remove the hook block (comment + 2 code lines)
    lines = content.splitlines()
    new_lines = []
    skip_next = 0
    for line in lines:
        if skip_next > 0:
            skip_next -= 1
            continue
        if line.strip() == HOOK_COMMENT:
            skip_next = 2  # skip the 2 code lines after comment
            continue
        # Also catch just the function line without comment
        if "_endless_prompt_hook" in line:
            continue
        if "precmd_functions+=(_endless_prompt_hook)" in line:
            continue
        new_lines.append(line)

    target.write_text("\n".join(new_lines) + "\n")

    click.echo(
        click.style("•", fg="cyan")
        + " Removed prompt hook from "
        + click.style(str(target), bold=True)
    )


# --- Claude Code hook ---

CLAUDE_SETTINGS_PATH = Path.home() / ".claude" / "settings.json"

# Events we want to hook into
CLAUDE_HOOK_EVENTS = [
    "SessionStart",
    "UserPromptSubmit",
    "PostToolUse",
    "Stop",
    "SessionEnd",
]


def _load_claude_settings() -> dict:
    if not CLAUDE_SETTINGS_PATH.exists():
        return {}
    with open(CLAUDE_SETTINGS_PATH) as f:
        return json.load(f)


def _save_claude_settings(settings: dict):
    CLAUDE_SETTINGS_PATH.parent.mkdir(parents=True, exist_ok=True)
    with open(CLAUDE_SETTINGS_PATH, "w") as f:
        json.dump(settings, f, indent=2)
        f.write("\n")


def _make_hook_entry(hook_bin: str, is_async: bool = True) -> dict:
    return {
        "hooks": [
            {
                "type": "command",
                "command": hook_bin + " claude",
                "async": is_async,
            }
        ]
    }

# Events that must be synchronous (Claude reads the response)
SYNC_EVENTS = {"SessionStart", "UserPromptSubmit", "PostToolUse"}


def _has_endless_hook(settings: dict) -> bool:
    hooks = settings.get("hooks", {})
    for event in CLAUDE_HOOK_EVENTS:
        entries = hooks.get(event, [])
        for entry in entries:
            for h in entry.get("hooks", []):
                if "endless-hook" in h.get("command", ""):
                    return True
    return False


def setup_claude_hook():
    hook_bin = _find_endless_hook()
    if not hook_bin:
        raise click.ClickException(
            "endless-hook binary not found on PATH. "
            "Build it first: go build -o /usr/local/bin/endless-hook "
            "./cmd/endless-hook/"
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Found endless-hook at {hook_bin}"
    )

    settings = _load_claude_settings()

    if _has_endless_hook(settings):
        click.echo(
            click.style("•", fg="cyan")
            + " Claude hook is already installed in "
            + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
        )
        return

    # Show what we'll add
    click.echo()
    click.echo(
        "The following hook events will be added to "
        + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
        + ":"
    )
    click.echo()
    for event in CLAUDE_HOOK_EVENTS:
        mode = "sync" if event in SYNC_EVENTS else "async"
        click.echo(
            f"  {click.style(event, fg='green')}"
            f" → {hook_bin} claude ({mode})"
        )
    click.echo()

    if not click.confirm("Proceed?", default=True):
        click.echo()
        click.echo(
            "To install manually, add these to "
            + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
            + " under \"hooks\":"
        )
        click.echo()
        for event in CLAUDE_HOOK_EVENTS:
            click.echo(f'  "{event}": '
                        f'[{{"hooks": [{{"type": "command", '
                        f'"command": "{hook_bin} claude", '
                        f'"async": true}}]}}]')
        click.echo()
        return

    # Add hooks to settings
    hooks = settings.setdefault("hooks", {})
    for event in CLAUDE_HOOK_EVENTS:
        is_async = event not in SYNC_EVENTS
        entry = _make_hook_entry(hook_bin, is_async=is_async)
        event_hooks = hooks.setdefault(event, [])
        already = any(
            "endless-hook" in h.get("command", "")
            for e in event_hooks
            for h in e.get("hooks", [])
        )
        if not already:
            event_hooks.append(entry)

    _save_claude_settings(settings)

    click.echo(
        click.style("•", fg="cyan")
        + " Claude hook installed in "
        + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
    )


def remove_claude_hook():
    settings = _load_claude_settings()
    if not _has_endless_hook(settings):
        click.echo(
            click.style("•", fg="cyan")
            + " No Endless hook found in "
            + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
        )
        return

    hooks = settings.get("hooks", {})
    for event in list(hooks.keys()):
        entries = hooks[event]
        hooks[event] = [
            entry for entry in entries
            if not any(
                "endless-hook" in h.get("command", "")
                for h in entry.get("hooks", [])
            )
        ]
        if not hooks[event]:
            del hooks[event]

    _save_claude_settings(settings)

    click.echo(
        click.style("•", fg="cyan")
        + " Removed Endless hook from "
        + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
    )
