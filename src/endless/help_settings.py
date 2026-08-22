"""Live setting values appended to the `--help` of the commands they govern.

E-2030 gated the GUIDE on `.endless/config.json`, so a project that switched the
report channel off is no longer routed to it. Command help is deliberately not
gated the same way: `endless minimizer --help` is reached by typing the command,
which means the reader is already asking about it, and hiding the description
would answer a direct question with silence.

What that help owed them instead is the setting. A reader who types
`endless minimizer --help` on a project where the loop is switched off gets a
faithful description of machinery that is not running, with nothing on the page
saying so. This module supplies the missing half: what the switch is, what it
resolves to HERE, and where it is written.

Resolved at help-render time from CWD, not baked in at import: one install
serves every project on the machine, and inside a worktree the answer comes from
the branch's own config rather than the project root's — which is precisely the
case where a reader would otherwise edit the wrong file.

Reads `config` directly rather than through `task_cmd._report_gate_on`: that
helper answers one question ("is the channel live?") for emitters that need a
bool, while this needs both switches AND the reason behind each, including the
distinction between "explicitly true" and "defaulted true" that the bool erases.
"""

from __future__ import annotations

from pathlib import Path

# Setting names this module can render, mapped to the commands that ask for one.
MINIMIZER = "minimizer"

_INDENT = "  "


def render(setting: str, cwd: Path | None = None) -> str | None:
    """The help section for `setting`, or None when there is nothing to say."""
    if setting != MINIMIZER:
        return None
    return _render_minimizer(cwd)


def _render_minimizer(cwd: Path | None = None) -> str:
    from endless import config

    root = config.enclosing_project_root(cwd)
    if root is None:
        return "\n".join([
            "Current setting:",
            f"{_INDENT}Not inside a registered project, so there is nothing to",
            f"{_INDENT}resolve. Both switches default ON; a project turns either",
            f"{_INDENT}off in its own .endless/config.json:",
            "",
            f'{_INDENT}    {{"minimizer": {{"enabled": true, "optimizer": false}}}}',
        ])

    start = Path.cwd() if cwd is None else cwd
    switches = config.minimizer_config_for_cwd(start)
    source = _source(root, start)

    # The heading deliberately does not name the project root. In a worktree the
    # value in force comes from the worktree's own config, not the root's, and
    # heading the block with the root would contradict the source line below it.
    lines = [
        "Current setting:",
        "",
        f"{_INDENT}minimizer.enabled    {_flag(switches['enabled'])}"
        f"   {_ENABLED_MEANING[switches['enabled']]}",
        f"{_INDENT}minimizer.optimizer  {_flag(switches['optimizer'])}"
        f"   {_OPTIMIZER_MEANING[switches['optimizer']]}",
        "",
        f"{_INDENT}{source}",
        f"{_INDENT}Both default on. `endless minimizer status` shows what the",
        f"{_INDENT}loop currently believes; this is only whether it may run.",
    ]
    return "\n".join(lines)


_ENABLED_MEANING = {
    True: "replies here go through the minimizer",
    False: "the report channel is off here",
}

_OPTIMIZER_MEANING = {
    True: "the loop may promote a new prompt",
    False: "the prompt is frozen at its champion",
}


def _flag(value: bool) -> str:
    """Fixed width so the two rows line up whichever way each resolves."""
    return "true " if value else "false"


def _source(root: Path, start: Path) -> str:
    """Which file the value came from — the fact a reader needs to CHANGE it.

    "true" alone does not say whether the answer was chosen or inherited, and
    those need different edits: one is a value to flip, the other is a key to
    add. Nor does it say WHERE, which matters most in a worktree: the nearest
    declaring config wins, so a session can be governed by its own branch's copy
    rather than by the project root it would think to edit.

    Walks the same layers as `config.minimizer_config_for_cwd` and stops at the
    first that declares, so the path named is the one actually in force.
    """
    from endless import config

    for parent in [start] + list(start.parents):
        cfg = _safe_read(parent)
        if cfg is None:
            if parent == root:
                break
            continue
        path = config.tilde(config.project_config_path(parent))
        if isinstance(cfg.get("minimizer"), (bool, dict)):
            return f'Set by "minimizer" in {path}.'
        if isinstance(cfg.get("report_gate"), bool):
            return (f'Set by "report_gate" in {path} — E-1953\'s name for '
                    f'"minimizer.enabled", still honored.')
        if parent == root:
            break

    path = config.tilde(config.project_config_path(root))
    return f'No "minimizer" key in {path} — both switches are at their default.'


def _safe_read(dir_path: Path) -> dict | None:
    """`project_config_read`, but a malformed file reads as absent, not a crash."""
    import json

    from endless import config

    try:
        return config.project_config_read(dir_path)
    except (OSError, json.JSONDecodeError):
        return None
