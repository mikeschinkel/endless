"""The minimizer's prompts (E-1771, rebuilt by E-1953, reobjectived by E-1975).

The prompt IS the product here, so it is a shipped asset with a user-editable
override layer rather than a string buried in the command.

Precedence mirrors the verbs.jsonl surface (E-1268): a registered project's
`.endless/report-prompts.jsonl` overrides the machine layer
`~/.config/endless/report-prompts.jsonl`, which overrides the embedded defaults
below. The file is JSONL, one record per line:

    {"name": "minimize", "text": "…"}
    {"name": "denylist", "text": "…"}

Only known names are honored; unknown names are ignored.

Three lifecycles now, and the third is the point of E-1975:

  * Editing an OVERRIDE needs no task and no land. That is the whole point of
    the layer — the wording takes substantial tuning, and routing every
    adjustment through ceremony would mean it never gets tuned.
  * PROMOTING an override into the embedded default below is where the ceremony
    lives, and it is gated on evidence: the override has to beat the current
    default over the persisted corpus.
  * The LOOP (`endless minimizer`) generates its own variants of the minimize
    text, replays them against the champion over a frozen corpus slice, and
    promotes by pointer move — no human in the path. The defaults below are the
    SEED it starts from and the floor it falls back to, not the thing it edits.
    A running loop means the prompt in force is `minimizer_variants`, not this
    file; `endless minimizer status` says which.

Why the anti-puffery half is SHIPPED rather than left to the user layer: it is a
stated product benefit, not personal configuration. "Endless filters out that
crap" has to be true out of the box or it is not a feature. The user layer holds
additions, not the baseline.

The minimizer is guidance to an adversarial reader, NOT a set of regex gates. A
gate would mangle a legitimate quotation of a banned phrase — a draft that says
`the user asked me to stop writing "load-bearing"` must survive intact.

WHAT CHANGED IN E-1975, since the shape of the prompt below only makes sense
against it (ED-1557). E-1953's minimizer could only DELETE, and was told so
explicitly. That produced its own defect, visible in the corpus: the keep-ratio
for drafts over 2k characters was 0.86 while 512-2k drafts sat around 0.62-0.67.
A long draft is long because it repeats — the task plan, the session status, the
paragraph three paragraphs up — and repetition is not deletable span by span
without leaving the prose ungrammatical. A deleting editor therefore keeps MORE
of the worst drafts, which is exactly backwards. Rewriting and deduplicating
against what the user already has is the fix, and it is why the prompt now takes
a fourth input: the context the fetch policy pulled.
"""

import json
from pathlib import Path

from endless import config

# The tunable entries. `minimize` is the instruction; `denylist` is the concrete
# anchor list spliced into it, kept separate so a user can extend the anchors
# without restating the whole instruction (and so a `$BLOAT "<phrase>"` label can
# append to it directly, turning an annoyance into config without a round trip).
#
# `judge` and `generate` join them under E-1975. They are prompts on the same
# footing — the judge decides what the loop believes, the generator decides what
# the loop tries — so hiding them in the command while `minimize` sat in a
# tunable layer would say the loop's own judgment is less worth tuning than the
# thing it judges.
MINIMIZE = "minimize"
DENYLIST = "denylist"
JUDGE = "judge"
GENERATE = "generate"

_KNOWN = (MINIMIZE, DENYLIST, JUDGE, GENERATE)

# The seed bypass threshold: a draft shorter than this skips the minimizer
# entirely.
#
# 256 was a guess in E-1953 and remains one. The corpus at design time said the
# SHORTEST real draft was 396 characters and that zero of 23 passed through
# untouched — so there is no evidence about behavior below any threshold,
# because nothing has ever been below one. That is precisely why it is an
# optimizer axis rather than a constant: latency and cost per turn are real, and
# the only way to learn where the floor belongs is to move it and watch.
DEFAULT_BYPASS_THRESHOLD = 256

# The seed fetch policy: what the minimizer is allowed to look at before it
# edits.
#
# This exists because ED-1557's dedup objective is unscoreable from the draft
# alone. "Remove what the user can already see in `session status`" cannot be
# judged, by a model or by a test, without `session status` in hand. Two ideas
# from the design turned out to be one mechanism: the minimizer fetches what it
# wants, AND the corpus row records what was requested and what came back. They
# are the same mechanism because a tool-using minimizer is nondeterministic in
# its INPUTS — an A/B over a frozen corpus is meaningless if each run re-fetches
# live state that has since moved — so fetching FORCES recording.
#
# "Give it everything every turn" fails on cost and latency, not on principle,
# which is exactly what makes the policy worth optimizing rather than fixing.
DEFAULT_FETCH_POLICY: dict = {
    "version": 1,
    # Each entry names a source from minimizer_fetch.SOURCES and when to run it.
    # `when` is "always" or "task" (only when the report names a task).
    "fetches": [
        {"source": "task_plan", "when": "task"},
        {"source": "session_status", "when": "always"},
    ],
    # Total budget across all sources. A policy that pulls 40k characters buys a
    # better dedup at a latency the user feels on every turn, so the ceiling is
    # part of the policy rather than a constant the loop cannot move.
    "max_chars": 6000,
}

# `minimize` takes four placeholders — {prompt} (what the user asked), {context}
# (what they already have), {draft} (the agent's whole reply) — plus {denylist},
# spliced from the entry below. Braces in an override are otherwise literal.
DEFAULTS: dict[str, str] = {
    MINIMIZE: (
        "You are an adversarial editor. An AI agent has drafted a reply to a "
        "user. Your job is to produce the reply the user will actually "
        "receive.\n"
        "\n"
        "You have two objectives, in this order.\n"
        "\n"
        "ONE: DELETE WHAT THE USER DID NOT ASK FOR. If the user asked for a "
        "discussion, a long discussion is correct and cutting it is a failure. "
        "If the user asked one question, one answer is correct and everything "
        "else goes, however well written.\n"
        "\n"
        "TWO: DO NOT MAKE THE USER READ THE SAME THING TWICE. Below is "
        "material they will have to review ANYWAY — what `session status` "
        "already shows them, and what is written into their task's plan, its "
        "analysis, a decision. Repeating it here does not inform them, it "
        "costs them a second reading of something they are going to open "
        "regardless. Cut it, and say instead where it already is if that is "
        "not obvious.\n"
        "\n"
        "This is about DURABLE material they will go and read. It is not about "
        "what you happened to say earlier in the conversation: chat is "
        "ephemeral, they are reading THIS message, and repeating something "
        "from an earlier reply here is not duplication.\n"
        "\n"
        "You may REWRITE, not only delete. Merge three paragraphs into one "
        "sentence. Replace jargon with the plain word. Turn a narrated process "
        "into its result. What you may never do is say something the draft did "
        "not say.\n"
        "\n"
        "=== WHAT THE USER ASKED ===\n"
        "{prompt}\n"
        "\n"
        "=== WHAT THE USER WILL REVIEW ANYWAY ===\n"
        "{context}\n"
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
        "3. A command the user is meant to RUN always survives, character for "
        "character — a verify command, a repro, an invocation they asked for. "
        "This is the single most common thing to lose and the most expensive: "
        "the user cannot reconstruct it.\n"
        "4. A DIRECT QUESTION GETS ITS DIRECT ANSWER. If the user asked "
        "something answerable, the answer is in your output, stated plainly and "
        "early. Never cut the answer and keep the context around it.\n"
        "5. Anything the user explicitly asked to see survives, even if it "
        "looks like ceremony to you.\n"
        "6. NEVER INVENT. Every claim in your output must be a claim the draft "
        "made. Rewriting is licence to compress what is there, never to add a "
        "conclusion, a number, a filename, or a reassurance the draft did not "
        "contain. A fabricated sentence is worse than every wasted one you were "
        "sent to remove.\n"
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
        "  - Anything restated from WHAT THE USER WILL REVIEW ANYWAY. A task's "
        "status, its plan, its children, what a sibling session is doing: it is "
        "one command away and they are going to look at it anyway.\n"
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
        "=== REWRITE ===\n"
        "\n"
        "For what survives:\n"
        "  - Replace jargon and invented terminology with the ordinary word. If "
        "no ordinary word exists, say the thing instead of naming it.\n"
        "  - Prefer the shortest form that keeps the meaning exact. Two "
        "sentences saying one thing become one sentence.\n"
        "  - Keep the user's own vocabulary where the draft used it.\n"
        "  - Keep the draft's structure — headings, lists, ordering — unless "
        "collapsing it removes repetition.\n"
        "\n"
        "=== BEFORE YOU OUTPUT ===\n"
        "\n"
        "Read your result against the draft and check, in this order:\n"
        "\n"
        "  1. Every command the draft told the user to RUN is present, "
        "character for character.\n"
        "  2. The direct answer to what they asked is present, and early.\n"
        "  3. Every table and fenced code block you kept is byte-identical to "
        "the draft's, fences included.\n"
        "\n"
        "If any of the three is missing, put it back before you answer. Cutting "
        "hard is the job; cutting these is the one failure that cannot be "
        "undone from the user's side.\n"
        "\n"
        "=== OUTPUT ===\n"
        "\n"
        "Output ONLY the edited reply, ready to send. No preamble, no "
        "explanation of your edits, no markers, no commentary about what you "
        "cut or why. Do not address the agent. Do not wrap the whole reply in a "
        "code fence.\n"
        "\n"
        "If the entire draft is content the user asked for and none of it is "
        "already theirs, output it unchanged. If nothing in it is, output the "
        "single most useful sentence it contains. Never output nothing."
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
    # The judge scores every turn, labelled or not. It is the only instrument
    # that can evaluate a variant over the WHOLE corpus — human labels are
    # sparse by construction, and always will be, because the user is a sensor
    # in the flow of work rather than an annotator.
    #
    # Human labels do not score prompts. They CALIBRATE this judge: it commits
    # to a prediction before seeing the user's reaction, and rolling
    # prediction-vs-outcome agreement is the loop's honesty check on itself.
    # Hence the two prediction fields, which are asked for BEFORE the scores so
    # the model commits rather than rationalizing backwards from them.
    JUDGE: (
        "You are scoring one edit. An adversarial minimizer was given a user's "
        "request, the material the user already had, and an AI agent's draft "
        "reply; it produced the minimized reply below. Decide how well it did.\n"
        "\n"
        "=== WHAT THE USER ASKED ===\n"
        "{prompt}\n"
        "\n"
        "=== WHAT THE USER WILL REVIEW ANYWAY ===\n"
        "{context}\n"
        "\n"
        "=== THE DRAFT ===\n"
        "{draft}\n"
        "\n"
        "=== THE MINIMIZED REPLY THE USER RECEIVED (option {slot}) ===\n"
        "{minimized}\n"
        "\n"
        "=== THE ALTERNATIVE THEY WERE SHOWN ALONGSIDE IT ===\n"
        "{alternative}\n"
        "\n"
        "Answer with ONE JSON object and nothing else:\n"
        "\n"
        "{\n"
        '  "will_complain": true|false,\n'
        '  "complaint_guess": "<the words you expect them to use, or \\"\\">",\n'
        '  "predicted_pick": "A" | "B" | null,\n'
        '  "fidelity": <0-100>,\n'
        '  "lost": ["<something the user needed that is gone>", …],\n'
        '  "invented": ["<a claim in the reply the draft never made>", …],\n'
        '  "redundant": ["<something still in the reply the user already had>", …]\n'
        "}\n"
        "\n"
        "Fill `will_complain`, `complaint_guess` and `predicted_pick` FIRST, "
        "before you have formed a view on the scores: they are PREDICTIONS about "
        "the human, and a prediction reasoned backwards from your own score "
        "measures nothing. Predict what THEY will do, not what you think is "
        "deserved.\n"
        "\n"
        "`predicted_pick` is null unless an alternative is shown above. When one "
        "is, name the option you expect the user to choose — which may be the "
        "one you would score lower. That disagreement is the single most useful "
        "thing you can record, because it is the only place your judgment can be "
        "caught being wrong.\n"
        "\n"
        "`fidelity` is whether the reply still does the job the draft was "
        "written to do — 100 when nothing the user needed was lost and nothing "
        "was fabricated, 0 when the answer to their question is gone. Length is "
        "NOT fidelity. A reply half the size that answers the question fully "
        "scores 100.\n"
        "\n"
        "`invented` is the one that matters most and the easiest to miss. The "
        "minimizer is allowed to rewrite, so it can silently assert something "
        "the draft never claimed. List every such claim, however plausible it "
        "sounds. An empty list means you checked, not that you did not look.\n"
        "\n"
        "`redundant` is material from WHAT THE USER WILL REVIEW ANYWAY. Removing it was the second half of the minimizer's job, "
        "so anything left there is a miss.\n"
        "\n"
        "Output the JSON object alone. No preamble, no code fence, no "
        "commentary."
    ),
    # The generator writes CHALLENGERS. It is seeded from published controlled
    # English standards rather than left to invent a style from nothing: those
    # grammars are decades of other people's evidence about what makes text
    # unambiguous and short, and a generator with no seed rediscovers "use
    # bullets" forever.
    #
    # It is told to diverge, not to comply. The seeds are a starting basis, and a
    # challenger that beats the champion by abandoning its seed entirely is
    # exactly the outcome worth having.
    GENERATE: (
        "You are tuning the prompt of an adversarial minimizer — the editor "
        "that turns an AI agent's draft reply into the reply a user actually "
        "receives. Write ONE challenger to the current champion.\n"
        "\n"
        "=== THE CHAMPION PROMPT ===\n"
        "{champion}\n"
        "\n"
        "=== HOW IT IS DOING ===\n"
        "{evidence}\n"
        "\n"
        "=== YOUR SEED THIS ROUND ===\n"
        "{seed}\n"
        "\n"
        "Take the seed as a starting basis and diverge from it wherever the "
        "evidence says to. It is a source of ideas, not a specification to "
        "comply with — a challenger that wins by abandoning its seed entirely "
        "is the best possible outcome.\n"
        "\n"
        "Hard requirements on the text you write:\n"
        "  - It MUST contain the placeholders {prompt}, {context}, {draft} and "
        "{denylist}, each at least once, or the prompt cannot be assembled.\n"
        "  - It MUST keep an invariants section covering: tables and fenced "
        "code survive byte for byte or are deleted whole; a command the user "
        "must run survives character for character; a direct question gets its "
        "direct answer; nothing is invented. These are checked mechanically "
        "after the fact, so a challenger that drops them does not get a lower "
        "score — it gets vetoed and wastes the round.\n"
        "  - It MUST tell the editor to output the reply alone, with no "
        "commentary.\n"
        "\n"
        "Change ONE thing meaningfully rather than rephrasing everything. A "
        "challenger that differs everywhere teaches nothing when it wins, "
        "because nobody can say which change did it.\n"
        "\n"
        "Answer with ONE JSON object and nothing else:\n"
        "\n"
        "{\n"
        '  "note": "<one line: what you changed and why>",\n'
        '  "prompt_text": "<the full challenger prompt>"\n'
        "}\n"
    ),
}

# Controlled-English grammars the generator is seeded from, one per round in
# rotation.
#
# Rotation rather than random choice: the loop should be able to say "we have
# tried each of these once" without a statistics argument, and a random seed
# makes an unlucky run indistinguishable from an exhausted search.
#
# The last two entries are not grammars at all. They are the orthogonal STYLE
# axis — a prompt can be written to any of these standards in prose or in
# bullets, and which of those the minimizer's OUTPUT should be is a separate
# question from how the instruction is worded.
GRAMMAR_SEEDS: tuple[str, ...] = (
    "ASD-STE100 (Simplified Technical English): one meaning per word, approved "
    "vocabulary, short sentences, active voice, no synonyms for a term already "
    "used.",
    "Attempto Controlled English (ACE): a strict, unambiguous subset of English "
    "with a deterministic reading of every sentence.",
    "Plain English Foundation standards: short sentences, familiar words, "
    "active voice, the point first, no nominalisations.",
    "CNL-P (Controlled Natural Language for Precision): constrained syntax "
    "aimed at removing ambiguity from specifications.",
    "RICECO / ICC controlled language: restricted grammar and terminology for "
    "operational instructions that must not be misread.",
    "PDL (Process Description Language): imperative, step-shaped, each "
    "instruction naming its actor and its object.",
    "STYLE AXIS — all-bullets: instruct the editor to emit bulleted output "
    "wherever the content permits, prose only where a bullet would break it.",
    "STYLE AXIS — prose: instruct the editor to emit continuous prose, using "
    "lists only for genuinely enumerable things.",
)


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


def build_minimize_prompt(
    prompts: dict[str, str],
    user_prompt: str,
    draft: str,
    context: str = "",
    minimize_text: str | None = None,
) -> str:
    """Splice the denylist and the turn's material into the minimize template.

    `minimize_text` overrides the configured `minimize` entry — that is how a
    variant under test is run, since the loop's champion lives in the DB rather
    than in the prompt layer.

    `replace` rather than `str.format` because the draft is arbitrary user
    content: a draft containing a JSON object, an f-string, or a shell brace
    expansion would make `format` raise KeyError or silently interpolate part of
    the agent's own text. The minimizer must never fail on the CONTENT of what it
    is minimizing.

    Order matters — the denylist is spliced first so that an override which
    inlines its own anchors is not re-substituted against the draft.
    """
    text = minimize_text if minimize_text is not None else prompts[MINIMIZE]
    text = text.replace("{denylist}", prompts.get(DENYLIST, ""))
    text = text.replace("{prompt}", user_prompt or "(not recorded)")
    text = text.replace("{context}", context or "(nothing fetched)")
    return text.replace("{draft}", draft)


def build_judge_prompt(
    prompts: dict[str, str],
    user_prompt: str,
    draft: str,
    minimized: str,
    context: str = "",
    alternative: str = "",
    slot: str = "",
) -> str:
    """Splice one turn into the judge template.

    `alternative` and `slot` are the paired half. They are passed rather than
    looked up because the judge is also run during replay, where the "pair" is
    two variants under test rather than two the user ever saw — and a prediction
    about a pick nobody was offered would poison the calibration window with
    predictions about an event that cannot occur.
    """
    text = prompts[JUDGE]
    text = text.replace("{prompt}", user_prompt or "(not recorded)")
    text = text.replace("{context}", context or "(nothing fetched)")
    text = text.replace("{slot}", slot or "—")
    text = text.replace(
        "{alternative}",
        alternative or "(none — this reply was not paired, so predicted_pick is null)")
    text = text.replace("{minimized}", minimized)
    return text.replace("{draft}", draft)


def build_generate_prompt(
    prompts: dict[str, str],
    champion: str,
    evidence: str,
    seed: str,
) -> str:
    """Splice the champion, its evidence and this round's seed into the
    generator template.

    The champion text is spliced LAST. It is the one input guaranteed to contain
    `{prompt}`, `{context}`, `{draft}` and `{denylist}` — they are what make it a
    minimize template — so substituting it earlier would have the later
    replacements rewrite the champion's own placeholders and hand the generator a
    prompt that no longer looks like the thing it is being asked to improve.
    """
    text = prompts[GENERATE]
    text = text.replace("{evidence}", evidence)
    text = text.replace("{seed}", seed)
    return text.replace("{champion}", champion)
