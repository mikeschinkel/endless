"""The minimizer prompt for `endless task report` (E-1771, rebuilt by E-1953).

The prompt IS the product here, so it is a shipped asset with a user-editable
override layer rather than a string buried in the command.

Precedence mirrors the verbs.jsonl surface (E-1268): a registered project's
`.endless/report-prompts.jsonl` overrides the machine layer
`~/.config/endless/report-prompts.jsonl`, which overrides the embedded defaults
below. The file is JSONL, one record per line:

    {"name": "minimize", "text": "…"}
    {"name": "denylist", "text": "…"}

Only known names are honored; unknown names are ignored.

Two lifecycles, deliberately different (ED-1552 / E-1952):

  * Editing an OVERRIDE needs no task and no land. That is the whole point of
    the layer — the wording will take substantial tuning, and routing every
    adjustment through ceremony would mean it never gets tuned.
  * PROMOTING an override into the embedded default below is where the ceremony
    lives, and it is gated on evidence: the override has to beat the current
    default over the persisted corpus (prompting message, raw draft, minimized
    output, and the user's `$CUT`/`$BLOAT`/`$WRONG`/`$GOOD` label). Promotion by
    feel is how a prompt slowly acquires everyone's pet phrasing.

Why the anti-puffery half is SHIPPED rather than left to the user layer: it is a
stated product benefit, not personal configuration. "Endless filters out that
crap" has to be true out of the box or it is not a feature. The user layer holds
additions, not the baseline.

The minimizer is guidance to an adversarial reader, NOT a set of regex gates. A
gate would mangle a legitimate quotation of a banned phrase — a draft that says
`the user asked me to stop writing "load-bearing"` must survive intact.
"""

import json
from pathlib import Path

from endless import config

# The tunable entries. `minimize` is the instruction; `denylist` is the concrete
# anchor list spliced into it, kept separate so a user can extend the anchors
# without restating the whole instruction (and so `$BLOAT "<phrase>"` can append
# to it directly, turning an annoyance into config without a round trip).
MINIMIZE = "minimize"
DENYLIST = "denylist"

_KNOWN = (MINIMIZE, DENYLIST)

# `minimize` takes two placeholders — {prompt} (what the user asked) and {draft}
# (the agent's whole reply) — plus {denylist}, spliced from the entry below.
# Braces in an override are otherwise literal.
DEFAULTS: dict[str, str] = {
    MINIMIZE: (
        "You are an adversarial editor. An AI agent has drafted a reply to a "
        "user. Your job is to cut it down to what the user actually asked for, "
        "and to output the result as the reply the user will receive.\n"
        "\n"
        "YOUR OBJECTIVE IS NOT TO MAKE IT SHORT. It is to DELETE WHAT THE USER "
        "DID NOT ASK FOR. Those are different, and the difference is the whole "
        "job. If the user asked for a discussion, a long discussion is correct "
        "and cutting it is a failure. If the user asked one question, one "
        "answer is correct and everything else goes, however well written.\n"
        "\n"
        "=== WHAT THE USER ASKED ===\n"
        "{prompt}\n"
        "\n"
        "=== THE AGENT'S DRAFT ===\n"
        "{draft}\n"
        "\n"
        "=== INVARIANTS — never violate these ===\n"
        "\n"
        "1. Markdown TABLES survive byte for byte, or are deleted whole. Never "
        "reformat, re-align, reorder, or partially trim a table.\n"
        "2. FENCED CODE BLOCKS survive byte for byte, fences included, or are "
        "deleted whole. Never reformat, re-indent, abbreviate, or elide code.\n"
        "3. A command the user is meant to RUN always survives — a verify "
        "command, a repro, an invocation they asked for. This is the single "
        "most common thing to lose and the most expensive: the user cannot "
        "reconstruct it.\n"
        "4. A DIRECT QUESTION GETS ITS DIRECT ANSWER. If the user asked "
        "something answerable, the answer is in your output, stated plainly and "
        "early. Never cut the answer and keep the context around it.\n"
        "5. Anything the user explicitly asked to see survives, even if it "
        "looks like ceremony to you.\n"
        "\n"
        "=== WHAT TO DELETE ===\n"
        "\n"
        "The generative rule, which governs everything below it:\n"
        "\n"
        "  DELETE ANY SENTENCE THAT CHARACTERIZES THE REASONING OR NARRATES THE "
        "ANALYSIS RATHER THAN DELIVERING INFORMATION THE USER NEEDS.\n"
        "\n"
        "Apply that rule first and always. The specifics below are anchors for "
        "it, not a checklist that replaces it — a draft will invent new ways to "
        "narrate itself that no list anticipates.\n"
        "\n"
        "Delete:\n"
        "  - Preamble and throat-clearing. Start at the first useful word.\n"
        "  - Restatements of what the user just said or asked.\n"
        "  - Announcements of what you are about to do, or just did.\n"
        "  - Self-assessment: how hard, clean, elegant, subtle, or interesting "
        "the work was. The user judges that.\n"
        "  - Recaps of state the user can look up themselves.\n"
        "  - Confirmations that a problem does NOT exist — \"no stray files\", "
        "\"nothing else was affected\", \"no regressions\". A clean result is "
        "reported by silence unless the user asked.\n"
        "  - Summaries of a thing that is itself directly above.\n"
        "  - Sign-offs, offers of further help, and closing flourishes.\n"
        "  - Hedging that carries no information (\"it's worth noting\", "
        "\"interestingly\").\n"
        "  - Consulting-speak and puffery, including these anchors:\n"
        "{denylist}\n"
        "\n"
        "=== OUTPUT ===\n"
        "\n"
        "Output ONLY the edited reply, ready to send. No preamble, no "
        "explanation of your edits, no markers, no commentary about what you "
        "cut or why. Do not address the agent. Do not wrap the whole reply in a "
        "code fence.\n"
        "\n"
        "Preserve the draft's own voice and formatting in what survives — you "
        "are deleting, not rewriting. Reword only where a deletion left a "
        "sentence ungrammatical.\n"
        "\n"
        "If the entire draft is content the user asked for, output it "
        "unchanged. If nothing in it is, output the single most useful sentence "
        "it contains. Never output nothing."
    ),
    # Anchors, not a gate. A denylist alone never converges — ban
    # "load-bearing" and you get "does the heavy lifting" — which is why it sits
    # UNDER a generative rule rather than standing in for one. The first three
    # entries are from the session that designed this command, which is the
    # honest place to draw them from.
    DENYLIST: (
        "      \"the thing that survives from your instinct\"\n"
        "      \"that reframes the decision\"\n"
        "      \"load-bearing\"\n"
        "      \"the key insight is\"\n"
        "      \"at its core\"\n"
        "      \"fundamentally\"\n"
        "      \"it's worth noting that\"\n"
        "      \"this is where it gets interesting\"\n"
        "      \"the real question is\"\n"
        "      \"deep dive\", \"unpack\", \"tease apart\"\n"
        "      \"robust\", \"seamless\", \"elegant\" as self-praise"
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
    """Return the prompt texts, applying project > machine > embedded."""
    prompts = dict(DEFAULTS)
    _read_layer(_machine_path(), prompts)
    pp = _project_path()
    if pp is not None:
        _read_layer(pp, prompts)
    return prompts


def build_minimize_prompt(prompts: dict[str, str], user_prompt: str, draft: str) -> str:
    """Splice the denylist and the turn's material into the minimize template.

    `replace` rather than `str.format` because the draft is arbitrary user
    content: a draft containing a JSON object, an f-string, or a shell brace
    expansion would make `format` raise KeyError or silently interpolate part of
    the agent's own text. The minimizer must never fail on the CONTENT of what it
    is minimizing.

    Order matters — the denylist is spliced first so that an override which
    inlines its own anchors is not re-substituted against the draft.
    """
    text = prompts[MINIMIZE].replace("{denylist}", prompts.get(DENYLIST, ""))
    text = text.replace("{prompt}", user_prompt or "(not recorded)")
    return text.replace("{draft}", draft)
