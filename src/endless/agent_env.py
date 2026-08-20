"""Identify which agent harness is running Endless (E-1962).

Python mirror of Go's `internal/agentenv`. Kept deliberately thin: the Go side
is where the hooks read it, and the hooks are the only thing gated on it. This
copy exists so `endless guide` can tell an unsupported harness to ignore
Endless, which is a Python command.

Named agent_env rather than agent because Endless already calls something else
an "agent": the background workers under an epic (`endless agents`). This module
is about the HOST running the session, not about those.

When the two sides disagree, the Go side wins — it is the one enforcing. Both
tables are transcriptions of the same observed environments, so they should only
diverge by mistake.
"""

import os
from typing import Callable

# Harness identities. Stable strings, because they are intended to become config
# and DB values (see E-1505, which needs a platform discriminator on the session
# row).
UNKNOWN = "unknown"
CLAUDE_CLI = "claude_cli"
CLAUDE_DESKTOP = "claude_desktop"

Lookup = Callable[[str], str]

_LABELS = {
    CLAUDE_CLI: "Claude Code (terminal)",
    CLAUDE_DESKTOP: "Claude Code Desktop",
}

# The harnesses Endless supports. An ALLOW-LIST: an unrecognized harness lands
# outside it, which is where everything except Claude Code CLI belongs. A
# deny-list would have to name every harness that exists, and missing one means a
# newly shipped host silently starts obeying contracts nobody chose for it.
_SUPPORTED = frozenset({CLAUDE_CLI})


ENTRYPOINT_VAR = "CLAUDE_CODE_ENTRYPOINT"
BUNDLE_VAR = "__CFBundleIdentifier"

CLI_ENTRYPOINT = "cli"
DESKTOP_ENTRYPOINT = "claude-desktop"
DESKTOP_BUNDLE_ID = "com.anthropic.claudefordesktop"


def _claude_cli(env: Lookup) -> bool:
    """Claude Code in a terminal.

    Observed 2026-08-13, Claude Code 2.1.222: CLAUDE_CODE_ENTRYPOINT=cli,
    CLAUDECODE=1, CLAUDE_CODE_SESSION_ID, AI_AGENT=claude-code_2-1-222_agent.

    Keyed on the entrypoint alone — it is the variable that names the surface,
    and Claude Code sets the others on surfaces this must not claim.
    """
    return env(ENTRYPOINT_VAR) == CLI_ENTRYPOINT


def _claude_desktop(env: Lookup) -> bool:
    """The Claude Code Desktop app.

    Observed 2026-08-13 by reading the Desktop harness process environment
    directly (`ps eww` on the bundled claude binary inside Claude.app, Claude
    Code 2.1.227): CLAUDE_CODE_ENTRYPOINT=claude-desktop,
    __CFBundleIdentifier=com.anthropic.claudefordesktop,
    CLAUDE_AGENT_SDK_VERSION=0.3.227.

    Desktop names itself in the entrypoint. An earlier version of this function
    asserted the opposite — that Desktop set no entrypoint, and that the absence
    was the signal — because the only sample then available came from Desktop's
    Bash tool and was incomplete. Sample the HARNESS process when re-deriving
    this: the Bash tool is a subprocess whose environment may differ from the one
    hooks inherit.

    Runs second so the CLI's positive match gets first refusal.
    """
    return (env(ENTRYPOINT_VAR) == DESKTOP_ENTRYPOINT
            or env(BUNDLE_VAR) == DESKTOP_BUNDLE_ID)


# Ordered most-specific first; the first claim wins. This is the extension
# point — a new harness is a row here plus an identity constant.
#
# There is deliberately no Codex CLI row yet. A detector never checked against a
# real dump of that harness's environment is a guess, and a guess here fails
# silently. Adding one is cheap once somebody pastes `env` from a live session;
# that, not code, is the missing input.
_DETECTORS = (
    (CLAUDE_CLI, _claude_cli),
    (CLAUDE_DESKTOP, _claude_desktop),
)


def detect(env: Lookup | None = None) -> str:
    """Identify the harness running this process."""
    if env is None:
        def env(key: str) -> str:
            return os.environ.get(key, "")
    for harness_id, claims in _DETECTORS:
        if claims(env):
            return harness_id
    return UNKNOWN


def supported(env: Lookup | None = None) -> bool:
    """Whether Endless supports the harness running this process.

    Prefer this over `detect() == CLAUDE_CLI`: same answer today, wrong answer
    the day a second harness is supported.
    """
    return detect(env) in _SUPPORTED


def present(env: Lookup | None = None) -> bool:
    """Whether an AGENT is running this process, as opposed to a person (E-2006).

    Deliberately not `supported()`: "an agent did this" and "Endless runs here"
    are different questions, and a Desktop agent is still an agent. Mirrors Go's
    `agentenv.Present`, which is in turn the same rule as a non-empty
    `events.Actor.Harness` on an event envelope.

    Exists because `detect() != UNKNOWN` was written out by hand at each of its
    call sites, with the same "not supported()" caveat re-argued in each
    docstring — the shape that let the Go side grow a second, disagreeing answer
    to this question in the first place.
    """
    return detect(env) != UNKNOWN


def label(harness_id: str) -> str:
    """Render a harness identity for a human.

    UNKNOWN gets a phrase rather than the bare slug because it reaches users.
    """
    return _LABELS.get(harness_id, "an unrecognized agent harness")
