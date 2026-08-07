"""User-editable prompt wording for `endless task report` (E-1771).

The reporting command's steering prompt and its two per-entry Haiku checks are
NOT hardcoded in the product — they live in a user-editable config file so that
when the agent freelances, the user tells it to improve the wording and the
agent edits the *config*, not product source (ED-1531, Requirement 5).

Precedence mirrors the verbs.jsonl surface (E-1268): a registered project's
`.endless/report-prompts.jsonl` overrides the machine layer
`~/.config/endless/report-prompts.jsonl`, which overrides the embedded
defaults. The file is JSONL, one record per line:

    {"name": "steer", "text": "…"}
    {"name": "nothing-to-report", "text": "…"}
    {"name": "note-check", "text": "…"}
    {"name": "question-check", "text": "…"}

E-1901 renamed `steer-empty` to `nothing-to-report`. The rename is the honest
description of a changed role: the text used to *steer* the agent toward
composing its own one-line sign-off when there were no facts, and now it IS the
computed block for that case — the thing the agent appends. No alias is kept for
the old name because nothing could depend on it: the override is opt-in and
neither layer's file existed.

E-1911 inverted what `steer` asks for: the agent's own answer is unconstrained
and the computed block is APPENDED to it after a fixed separator. The separator
itself is deliberately NOT a prompt entry — see `SEPARATOR` below.

Only the four known names are honored; unknown names are ignored. A record with
a known name replaces the lower-precedence text for that name.

The Haiku *model* is fixed (not tunable) — only the wording is a lever (Req 4).
"""

import json
from pathlib import Path

from endless import config

# The four tunable prompts. `steer` frames the computed block the agent must
# append; `nothing-to-report` IS the computed block when nothing was computed
# (E-1901); `note-check` / `question-check` each classify one free-text entry,
# and MUST instruct the model to answer with a leading KEEP / DROP token (see
# report_cmd parsing).
STEER = "steer"
NOTHING_TO_REPORT = "nothing-to-report"
NOTE_CHECK = "note-check"
QUESTION_CHECK = "question-check"

_KNOWN = (STEER, NOTHING_TO_REPORT, NOTE_CHECK, QUESTION_CHECK)

# The line that OPENS the appended block (E-1911). One marker, not a pair: the
# block runs to the end of the agent's message by construction, so a closing
# marker would delimit nothing. It is therefore also why the command prints the
# block last — anything printed after the separator would be inside the block.
#
# Fixed literal, and deliberately NOT one of the tunable prompt entries: this is
# the string a validator (and the user) matches on to find the block, and a
# machine-detectable marker that a per-machine config could override away is not
# machine-detectable. `report_cmd` prints it directly rather than interpolating
# it into `steer`, so overriding the steer wording cannot lose it.
#
# Mirrored by `reportSeparator` in internal/hookcmd/claude.go, which names it in
# the compose-time nudge. TestReportSeparatorMatchesPython pins the two together.
SEPARATOR = "----- ENDLESS REPORT -----"

# The check texts take `{text}` — the single entry under classification.
# `steer` and `nothing-to-report` take NO placeholder: the steer is instruction
# only (the command prints the separator and the block after it), and
# `nothing-to-report` is used verbatim as the block text. Braces in an override
# of either are literal.
DEFAULTS: dict[str, str] = {
    STEER: (
        "Answer the user in your own words first. That half of your reply is "
        "NOT constrained by this block — say what the turn actually calls for, "
        "at whatever length it calls for.\n"
        "\n"
        "Then APPEND the block printed below to the END of that reply, "
        "unchanged, starting with its separator line. Reproduce the separator "
        "and the block exactly as printed: do not edit, summarize, reorder, or "
        "comment on the block, and write nothing after it.\n"
        "\n"
        "If the block is the single line `Nothing to report.`, append it "
        "anyway. That line is the report's null result; dropping it is "
        "indistinguishable from a block that failed to render.\n"
        "\n"
        "If a fact belongs inside the block and is missing, do not hand-write "
        "it there — re-run `endless task report` with a --json note, question, "
        "or verify entry so the command computes it, then append the new block."
    ),
    NOTHING_TO_REPORT: "Nothing to report.",
    NOTE_CHECK: (
        "An agent is filing a handoff NOTE for a human reviewer. A GOOD note "
        "states a real thing the reviewer could NOT compute from git or the "
        "task tracker — an out-of-band fact or a genuine anomaly. A BAD note is "
        "ceremony: it confirms the ABSENCE of a problem, restates something "
        "already visible in git/task state, or is self-congratulatory.\n"
        "\n"
        'NOTE: "{text}"\n'
        "\n"
        'Reply "KEEP" if it is a genuine non-computable fact. Reply '
        '"DROP: <short reason>" if it is ceremony or already computable. '
        "Do not rationalize a ceremonial note into a real one to let it pass."
    ),
    QUESTION_CHECK: (
        "An agent is filing a handoff QUESTION for the user. A GOOD question is "
        "a real decision the user must make before the work can proceed or "
        "land. A BAD question restates settled state, asks the user to confirm "
        "something already decided, or is rhetorical.\n"
        "\n"
        'QUESTION: "{text}"\n'
        "\n"
        'Reply "KEEP" if it is a genuine open decision the user needs to make. '
        'Reply "DROP: <short reason>" if it restates settled state or is '
        "ceremony. Do not invent a decision to let a settled-state question pass."
    ),
}


def _read_layer(path: Path, out: dict[str, str]) -> None:
    """Merge known-name records from a JSONL layer into `out` (in place)."""
    if not path.exists():
        return
    try:
        lines = path.read_text().splitlines()
    except OSError:
        return
    for line in lines:
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        name = rec.get("name")
        text = rec.get("text")
        if name in _KNOWN and isinstance(text, str):
            out[name] = text


def _machine_path() -> Path:
    return config.CONFIG_DIR / "report-prompts.jsonl"


def _project_path() -> Path | None:
    """Registered project's `.endless/report-prompts.jsonl`, or None.

    Reuses the verbs-file resolver (same `.endless` dir, same project lookup)
    so the two config surfaces always agree on which project's tree wins.
    """
    from endless import matchers
    vp = matchers.project_verbs_path()
    if vp is None:
        return None
    return vp.with_name("report-prompts.jsonl")


def load_prompts() -> dict[str, str]:
    """Return the three prompt texts, applying project > machine > embedded."""
    prompts = dict(DEFAULTS)
    _read_layer(_machine_path(), prompts)
    pp = _project_path()
    if pp is not None:
        _read_layer(pp, prompts)
    return prompts
