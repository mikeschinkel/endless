"""Agent-facing CLI output: `--help` augmentation, and refusals.

When a Claude Code agent (or a human passing `--agent-view`) runs `<cmd> --help`,
prepend a directive pointing at the guide section that explains the command,
above Click's normal help. The agent reached for `--help`, so the signpost to
the guide lives there. The map file *points*; the guide section *explains* —
no duplication, so nothing drifts.

`agent_error` (E-2097) is the same audience seen from the other side: an agent
that pipes a refusal through `tail -3` keeps the end of the guidance and never
sees the verdict, so a refusal rendered for an agent repeats its one-line
verdict at BOTH ends. Both surfaces ask the same question — is an agent reading
this? — and `agent_facing` is the one place that answers it.

This module has no dependency on `cli`; the concrete `AgentAware*` Click classes
are defined in `cli.py` (where `DBAwareGroup` lives) by mixing `AgentHelpMixin`
in. `_AGENT_VIEW` is set by the root group's argv pre-scan when `--agent-view`
appears in any position (mirroring how `--db` is consumed).
"""

from __future__ import annotations

import os

import click

from endless import agent_env
from endless.guide_map import load_map

_AGENT_VIEW = False


def set_agent_view(value: bool) -> None:
    global _AGENT_VIEW
    _AGENT_VIEW = value


def agent_view_requested() -> bool:
    return _AGENT_VIEW


def agent_facing() -> bool:
    """An agent is reading this output, or a human asked to see what one sees.

    Public since E-2097, which needed the same question answered for refusals
    (`agent_error`) as for `--help`. It stayed private while `--help` was the
    only caller; a second caller made "reuse this" the whole point, since the
    alternative is a fresh spelling of a question this module has already
    consolidated twice.

    Harness detection is `agent_env`'s job (E-1962). This module used to carry
    its own `is_claude_code_agent()` keyed on CLAUDECODE=1 — a third spelling
    of the same question, alongside `task_cmd._running_under_agent()` and the
    detector itself. Folded in E-1966: one detector, no copies that can drift
    apart. E-2006 folded the last of it — the `!= UNKNOWN` comparison itself,
    which was still written out here and in `_running_under_agent` — into
    `agent_env.present`. E-2097 retired `_running_under_agent` too: both its
    callers wanted this question, and one of them was already spelling it out
    as `_running_under_agent() or agent_view_requested()`.
    """
    return agent_env.present() or _AGENT_VIEW


def _command_path(ctx: click.Context) -> str:
    """ctx.command_path is 'endless task spawn'; drop the leading prog name."""
    parts = ctx.command_path.split(" ", 1)
    return parts[1] if len(parts) > 1 else ""


def current_command_path() -> str | None:
    """'task add' for `endless task add`, or None outside a running command.

    Lets a refusal name the verb that produced it without every validator
    threading its own command string down from the CLI layer — the validators
    are shared by several verbs, so the string they would thread is the one
    thing they cannot know.
    """
    ctx = click.get_current_context(silent=True)
    if ctx is None:
        return None
    return _command_path(ctx) or None


# The stable marker that makes one line of an agent's scrollback identifiable
# as an Endless refusal without the surrounding message. It has to carry the
# project name: the command alone does not identify us — `task add` is also
# Taskwarrior's verb — and a bare "ERROR:" is in every tool's output, so it
# greps to noise.
#
# It does NOT repeat the word "error". Click already supplies that (see
# _CLICK_ERROR_PREFIX), and the line reads as one sentence rather than two:
#
#     Error: [Endless] task add: title 107>100 chars. …
ERROR_SENTINEL = "[Endless]"

# Click prints a ClickException as `Error: <message>`, so the message's first
# line arrives with that prefix and the bracket's two ends would not match byte
# for byte unless the closing copy carries it too. The coupling is deliberate
# and pinned by tests/test_agent_error_bracket.py, which compares the RENDERED
# first and last lines rather than the message string.
_CLICK_ERROR_PREFIX = "Error: "


def agent_error(summary: str, guidance: str, command: str | None = None) -> str:
    """Bracket a refusal with a repeated one-line verdict, for agents (E-2097).

    Returns the message to hand to `click.ClickException`.

    For a human, that is `guidance` unchanged — byte for byte today's message.
    A repeated long line is noise to a reader who was never going to truncate
    it, so the duplication is agent-only by design.

    For an agent, the same guidance arrives bracketed:

        [Endless] task add: <summary>

        … guidance, unchanged …

        Error: [Endless] task add: <summary>

    The two verdict lines are IDENTICAL on purpose. Split them — problem first,
    remedy last — and `head -N` yields the problem without the fix while
    `tail -N` yields the fix without the problem. Identical means whichever end
    a truncating pipe leaves is sufficient alone, and a reader who sees both
    reads one repeat rather than two findings.

    `summary` must be ONE line and carry the whole verdict: the measured
    numbers, where the content belongs instead, and whether anything changed.
    Length is not a constraint — `head`/`tail` are line-based, so a
    200-character line survives whole — but a paragraph is, because a paragraph
    invites exactly the skimming this exists to defeat.
    """
    if not agent_facing():
        return guidance
    if command is None:
        command = current_command_path()
    verdict = (f"{ERROR_SENTINEL} {command}: {summary}" if command
               else f"{ERROR_SENTINEL}: {summary}")
    return f"{verdict}\n\n{guidance.rstrip()}\n\n{_CLICK_ERROR_PREFIX}{verdict}"


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
        # Just the directive — the agent is being told which section to read,
        # not choosing among sections, so the section's `covers` summary adds
        # noise here. `covers` earns its place in the index table (`endless
        # guide`), where you scan to *find* the section. Command-specific notes
        # stay: they're not in the guide.
        for slug in entry.sections:
            lines.append(f"    endless guide {slug}")
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
        if agent_facing():
            try:
                block = agent_block(ctx)
            except Exception:
                block = None
            if block:
                formatter.write(block)
                formatter.write("\n")
        super().format_help(ctx, formatter)
