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
sanctioned message for that case — the thing the agent relays, and the string the
Stop gate compares against. No alias is kept for the old name because nothing
could depend on it: the override is opt-in and neither layer's file existed.

Only the four known names are honored; unknown names are ignored. A record with
a known name replaces the lower-precedence text for that name.

The Haiku *model* is fixed (not tunable) — only the wording is a lever (Req 4).
"""

import json
from pathlib import Path

from endless import config

# The four tunable prompts. `steer` frames the sanctioned block the agent must
# relay; `nothing-to-report` IS the sanctioned block when nothing was computed
# (E-1901); `note-check` / `question-check` each classify one free-text entry,
# and MUST instruct the model to answer with a leading KEEP / DROP token (see
# report_cmd parsing).
STEER = "steer"
NOTHING_TO_REPORT = "nothing-to-report"
NOTE_CHECK = "note-check"
QUESTION_CHECK = "question-check"

_KNOWN = (STEER, NOTHING_TO_REPORT, NOTE_CHECK, QUESTION_CHECK)

# The delimiters the steer wraps around the sanctioned block. They give
# "verbatim" an unambiguous extent — without them "add nothing else" has to be
# judged against a boundary the agent infers, which is exactly the judgment call
# the E-1901 gate removes. The Stop gate strips these from both sides before
# comparing, so relaying them is tolerated rather than bounced.
BEGIN_MARKER = "----- BEGIN REPORT -----"
END_MARKER = "----- END REPORT -----"

# `{facts}` in the steer text is replaced with the sanctioned block. The check
# texts take `{text}` — the single entry under classification.
# `nothing-to-report` takes NO placeholder: it is used verbatim as the sanctioned
# text, so braces in an override are literal.
DEFAULTS: dict[str, str] = {
    STEER: (
        "Relay the block between the markers below as your ENTIRE final "
        "message — byte for byte, nothing before it, nothing after it. No "
        "preamble, no sign-off, no success confirmation, no remark about "
        "anything absent from it.\n"
        "\n"
        "This is enforced, not advisory: a Stop hook compares your final "
        "message against this block and blocks the turn if you appended to it, "
        "naming the violation to you AND to the user.\n"
        "\n"
        "If something needs saying that is not in the block, it does not go in "
        "your reply — re-run `endless task report` with a --json note, "
        "question, or verify entry so it lands INSIDE the block, then relay the "
        "new block.\n"
        "\n"
        f"{BEGIN_MARKER}\n"
        "{facts}\n"
        f"{END_MARKER}"
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
