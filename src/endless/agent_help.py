"""Agent-facing `--help` augmentation.

When a Claude Code agent (or a human passing `--agent-view`) runs `<cmd> --help`,
prepend a directive pointing at the guide section that explains the command,
above Click's normal help. The agent reached for `--help`, so the signpost to
the guide lives there. The map file *points*; the guide section *explains* —
no duplication, so nothing drifts.

This module has no dependency on `cli`; the concrete `AgentAware*` Click classes
are defined in `cli.py` (where `DBAwareGroup` lives) by mixing `AgentHelpMixin`
in. `_AGENT_VIEW` is set by the root group's argv pre-scan when `--agent-view`
appears in any position (mirroring how `--db` is consumed).
"""

from __future__ import annotations

import os

import click

from endless.guide_map import load_map

_AGENT_VIEW = False


def is_claude_code_agent() -> bool:
    """Is the current process running inside a Claude Code agent harness?

    Today: Claude Code (sets CLAUDECODE=1). Extend as other harnesses appear.
    """
    return os.environ.get("CLAUDECODE") == "1"


def set_agent_view(value: bool) -> None:
    global _AGENT_VIEW
    _AGENT_VIEW = value


def agent_view_requested() -> bool:
    return _AGENT_VIEW


def _should_augment() -> bool:
    return is_claude_code_agent() or _AGENT_VIEW


def _command_path(ctx: click.Context) -> str:
    """ctx.command_path is 'endless task spawn'; drop the leading prog name."""
    parts = ctx.command_path.split(" ", 1)
    return parts[1] if len(parts) > 1 else ""


def agent_block(ctx: click.Context) -> str | None:
    """The directive text to prepend, or None when there's nothing to add."""
    cmd_path = _command_path(ctx)
    if not cmd_path:
        return None  # the root group already points at the guide via its content

    entry = load_map(cmd_path)
    header = ("\033[1m▸ AGENT — read this before using this command:\033[0m"
              if _color(ctx) else "▸ AGENT — read this before using this command:")
    lines = [header]

    if entry is None:
        lines.append("    No guide section is mapped to this command yet. Run "
                     "`endless guide` for the index.")
    elif entry.sections:
        for slug in entry.sections:
            covers = f"   ({entry.covers})" if entry.covers else ""
            lines.append(f"    endless guide {slug}{covers}")
        if entry.note:
            for note_line in entry.note.splitlines():
                lines.append(f"    {note_line}")
    elif entry.gap:
        lines.append(f"    No guide section covers this yet — {entry.gap}")
        lines.append("    Run `endless guide` for the index.")
    else:
        lines.append("    Run `endless guide` for the index.")
    return "\n".join(lines) + "\n"


def _color(ctx: click.Context) -> bool:
    try:
        return ctx.color is not False and os.isatty(1)
    except Exception:
        return False


class AgentHelpMixin:
    """Mixin for Click Command/Group: prepend the agent block to --help.

    Mixed in by `AgentAwareCommand`/`AgentAwareGroup` in cli.py. Never raises
    into help rendering — a missing/garbled map file degrades to no block.
    """

    def format_help(self, ctx, formatter):  # type: ignore[override]
        if _should_augment():
            try:
                block = agent_block(ctx)
            except Exception:
                block = None
            if block:
                formatter.write(block)
                formatter.write("\n")
        super().format_help(ctx, formatter)
