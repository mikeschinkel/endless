"""Setup commands — install hooks and plugins into shell/tool configs."""

import json
import shutil
from pathlib import Path

import click

HOOK_COMMENT = "# Endless: project activity monitor (prompt hook)"
HOOK_CODE = """\
_endless_prompt_hook() {
  (endless-go hook prompt "$PWD" &>/dev/null &)
}
precmd_functions+=(_endless_prompt_hook)"""

HOOK_BLOCK = f"{HOOK_COMMENT}\n{HOOK_CODE}"

DEFAULT_ZSHRC = Path.home() / ".zshrc"


def _find_endless_hook() -> str | None:
    """Check if endless-go binary is on PATH (provides the `hook` subcommand)."""
    return shutil.which("endless-go")


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
            "endless-go binary not found on PATH."
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Found endless-go at {hook_bin}"
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


# --- Session shell helpers (esu/esp/esf) ---

SHELL_HELPERS_COMMENT = "# Endless: session shell helpers (esu/esp/esf)"
SHELL_HELPERS_EVAL = 'eval "$(endless shell-init)"'
SHELL_HELPERS_BLOCK = f"{SHELL_HELPERS_COMMENT}\n{SHELL_HELPERS_EVAL}"


def _file_contains_shell_helpers(path: Path) -> bool:
    """Check if a file already installs the Endless session shell helpers."""
    if not path.exists():
        return False
    content = path.read_text()
    return SHELL_HELPERS_COMMENT in content or SHELL_HELPERS_EVAL in content


def install_shell_helpers():
    """Install the esu/esp/esf session shell helpers into a ZSH startup file.

    Appends an `eval "$(endless shell-init)"` line (not the static snippet) so
    helper fixes reach the user on next shell launch with no rc surgery.
    """
    click.echo(
        "The session shell helpers (esu/esp/esf) need to be added to your "
        "ZSH startup file."
    )
    click.echo(
        "The standard location is "
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
    if _file_contains_shell_helpers(target):
        click.echo(
            click.style("•", fg="cyan")
            + " Session shell helpers are already installed in "
            + click.style(str(target), bold=True)
        )
        return

    # Show what we'll add
    click.echo()
    click.echo("The following will be added to "
               + click.style(str(target), bold=True) + ":")
    click.echo()
    for line in SHELL_HELPERS_BLOCK.splitlines():
        click.echo(f"  {click.style(line, fg='green')}")
    click.echo()

    if not click.confirm("Proceed?", default=True):
        # Show manual instructions instead
        click.echo()
        click.echo("To install manually, add this to your "
                    "ZSH startup file:")
        click.echo()
        for line in SHELL_HELPERS_BLOCK.splitlines():
            click.echo(f"  {line}")
        click.echo()
        return

    # Append to file
    content = target.read_text() if target.exists() else ""
    if content and not content.endswith("\n"):
        content += "\n"
    content += f"\n{SHELL_HELPERS_BLOCK}\n"
    target.write_text(content)

    click.echo(
        click.style("•", fg="cyan")
        + " Session shell helpers installed in "
        + click.style(str(target), bold=True)
    )
    click.echo(
        click.style("•", fg="cyan")
        + " Run "
        + click.style(f"source {target}", bold=True)
        + " or open a new terminal to activate."
    )


def remove_shell_helpers():
    """Remove the Endless session shell helpers from a ZSH startup file."""
    click.echo(
        "Enter the path to the file containing "
        "the Endless session shell helpers:"
    )
    target_str = click.prompt(
        "Path", default=str(DEFAULT_ZSHRC),
    )
    target = Path(target_str).expanduser()

    if not target.exists():
        raise click.ClickException(f"File not found: {target}")

    if not _file_contains_shell_helpers(target):
        click.echo(
            click.style("•", fg="cyan")
            + " No Endless session shell helpers found in "
            + click.style(str(target), bold=True)
        )
        return

    content = target.read_text()
    lines = content.splitlines()
    new_lines = []
    for line in lines:
        if line.strip() == SHELL_HELPERS_COMMENT:
            continue
        if line.strip() == SHELL_HELPERS_EVAL:
            continue
        new_lines.append(line)

    target.write_text("\n".join(new_lines) + "\n")

    click.echo(
        click.style("•", fg="cyan")
        + " Removed session shell helpers from "
        + click.style(str(target), bold=True)
    )


# --- Claude Code hook ---

CLAUDE_SETTINGS_PATH = Path.home() / ".claude" / "settings.json"

# Events we want to hook into
CLAUDE_HOOK_EVENTS = [
    "PreToolUse",
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
                "command": hook_bin + " hook claude",
                "async": is_async,
            }
        ]
    }

# Events that must be synchronous (Claude reads the response).
#
# Stop joined this set in E-1901: an async hook runs in the background and the
# stop proceeds immediately, so it cannot return decision:"block". The
# verbatim-relay gate blocks at Stop, and would silently do nothing if the hook
# were installed async — the worst kind of failure, since every other symptom
# (hook fires, DB row written, no error anywhere) says it is working.
#
# Stop STAYS here now that E-1911 has parked that gate. The park is a code-level
# constant at the gate's own call site precisely so this file does not move: a
# config-level disable would drift per machine and per worktree, and would
# silently un-park itself on the next `setup` run. Reviving the gate must not
# also require re-discovering that Stop has to be synchronous.
SYNC_EVENTS = {"PreToolUse", "SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"}


def _has_endless_hook(settings: dict) -> bool:
    hooks = settings.get("hooks", {})
    for event in CLAUDE_HOOK_EVENTS:
        entries = hooks.get(event, [])
        for entry in entries:
            for h in entry.get("hooks", []):
                if "endless-go" in h.get("command", ""):
                    return True
    return False


def _repair_hook_async_flags(settings: dict) -> list[str]:
    """Correct endless-go hook entries whose `async` flag disagrees with SYNC_EVENTS.

    Needed because `setup_claude_hook` returns early once a hook is installed, so
    an existing install would never pick up a sync/async change — E-1901 flipped
    Stop to sync, and without this every machine already running Endless would
    keep a non-blocking Stop hook forever while reporting itself correctly set up.

    Mutates `settings` in place and returns the event names it changed (empty
    when already correct, which makes it idempotent and safe to call on every
    setup run).
    """
    repaired: list[str] = []
    hooks = settings.get("hooks", {})
    for event in CLAUDE_HOOK_EVENTS:
        want_async = event not in SYNC_EVENTS
        for entry in hooks.get(event, []):
            for h in entry.get("hooks", []):
                if "endless-go" not in h.get("command", ""):
                    continue
                if h.get("async") != want_async:
                    h["async"] = want_async
                    repaired.append(event)
    return repaired


def setup_claude_hook():
    hook_bin = _find_endless_hook()
    if not hook_bin:
        raise click.ClickException(
            "endless-go binary not found on PATH."
        )

    click.echo(
        click.style("•", fg="cyan")
        + f" Found endless-go at {hook_bin}"
    )

    settings = _load_claude_settings()

    if _has_endless_hook(settings):
        click.echo(
            click.style("•", fg="cyan")
            + " Claude hook is already installed in "
            + click.style(str(CLAUDE_SETTINGS_PATH), bold=True)
        )
        repaired = _repair_hook_async_flags(settings)
        if repaired:
            _save_claude_settings(settings)
            click.echo(
                click.style("•", fg="yellow")
                + " Repaired sync/async flag for: "
                + click.style(", ".join(sorted(set(repaired))), bold=True)
            )
            click.echo(
                "  Restart any running Claude sessions for this to take effect."
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
            f" → {hook_bin} hook claude ({mode})"
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
            # Per-event, not a blanket true: a hand-installed async Stop hook
            # cannot block, silently disabling the E-1901 relay gate.
            flag = "false" if event in SYNC_EVENTS else "true"
            click.echo(f'  "{event}": '
                        f'[{{"hooks": [{{"type": "command", '
                        f'"command": "{hook_bin} hook claude", '
                        f'"async": {flag}}}]}}]')
        click.echo()
        return

    # Add hooks to settings
    hooks = settings.setdefault("hooks", {})
    for event in CLAUDE_HOOK_EVENTS:
        is_async = event not in SYNC_EVENTS
        entry = _make_hook_entry(hook_bin, is_async=is_async)
        event_hooks = hooks.setdefault(event, [])
        already = any(
            "endless-go" in h.get("command", "")
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
                "endless-go" in h.get("command", "")
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


# --- Output style (E-1919) ---------------------------------------------------
#
# The style file itself is embedded in the Go binary and materialized by
# `endless-go outputstyle`. These wrappers exist so the install has a home in
# the `setup` group alongside claude-hook/prompt-hook/shell-helpers, each of
# which has a remove- counterpart. They stream the Go command's own output
# rather than reformatting it, so the not-activated warning reaches the user
# verbatim.


def _run_outputstyle(args: list[str], cwd: "Path | None" = None) -> int:
    """Invoke `endless-go outputstyle <args>`, streaming stdout/stderr through.

    Prefers the worktree-built binary when running inside a self-dev worktree
    (its embedded style is the candidate code under test); falls back to the
    PATH-resolved global otherwise.
    """
    import subprocess

    from endless import config

    go_bin = config.resolved_worktree_endless_go()
    if go_bin is None or not go_bin.exists():
        found = shutil.which("endless-go")
        if not found:
            raise click.ClickException("endless-go binary not found on PATH.")
        go_bin = Path(found)
    try:
        result = subprocess.run(
            [str(go_bin), "outputstyle", *args],
            cwd=str(cwd) if cwd else None,
        )
    except (FileNotFoundError, OSError) as e:
        raise click.ClickException(f"endless-go failed: {e}")
    return result.returncode


def setup_output_style(activate: bool = False, project: str | None = None,
                       force: bool = False, cwd: Path | None = None) -> None:
    """Install the Endless Claude Code output style into the project."""
    args = ["install"]
    if activate:
        args.append("--activate")
    if force:
        args.append("--force")
    if project:
        args.extend(["--project", project])
    code = _run_outputstyle(args, cwd=cwd)
    if code != 0:
        raise click.ClickException("endless-go outputstyle install failed")


def remove_output_style(project: str | None = None) -> None:
    """Remove the Endless output style from the project and deactivate it."""
    args = ["remove"]
    if project:
        args.extend(["--project", project])
    code = _run_outputstyle(args)
    if code != 0:
        raise click.ClickException("endless-go outputstyle remove failed")
