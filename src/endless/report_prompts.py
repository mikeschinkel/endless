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
    {"name": "note-check", "text": "…"}
    {"name": "question-check", "text": "…"}

Only the three known names are honored; unknown names are ignored. A record with
a known name replaces the lower-precedence text for that name.

The Haiku *model* is fixed (not tunable) — only the wording is a lever (Req 4).
"""

import json
from pathlib import Path

from endless import config

# The three tunable prompts. `steer` frames the final message the agent prints;
# `note-check` / `question-check` each classify one free-text entry, and MUST
# instruct the model to answer with a leading KEEP / DROP token (see
# report_cmd parsing).
STEER = "steer"
NOTE_CHECK = "note-check"
QUESTION_CHECK = "question-check"

_KNOWN = (STEER, NOTE_CHECK, QUESTION_CHECK)

# `{facts}` in the steer text is replaced with the computed fact block. The
# check texts take `{text}` — the single entry under classification.
DEFAULTS: dict[str, str] = {
    STEER: (
        "Report the following to the user as your final message, and add "
        "nothing else. State only these facts, plainly. Do NOT add a preamble, "
        "a sign-off, success confirmations, or any remark about categories that "
        "are absent below — if something is not listed, say nothing about it.\n"
        "\n"
        "{facts}"
    ),
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
